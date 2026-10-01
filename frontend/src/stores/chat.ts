import { defineStore } from 'pinia'
import { computed, reactive, ref } from 'vue'
import {
  bridge,
  openExternal,
  type ChatMessageDTO,
  type ChatToolEventDTO,
  type RerunResultDTO,
  type RevertResultDTO,
  type SessionSummaryDTO,
} from '../wails'
import { errText } from '../composables/errText'
import { useWorkspaceStore } from './workspace'

// 任务清单单项（todo 工具的全量快照）
export interface TodoItem {
  text: string
  status: 'pending' | 'in_progress' | 'done'
}

// 会话消息的 UI 形态；streaming 标记流式中的临时消息，error 标记错误/取消态；
// role='tool' 为工具卡片（toolName/status 承载卡片数据）；
// at 为消息时间戳（渲染 HH:MM）；term 记录终态枚举（UI 区分 取消/超时 与 错误）。
export interface ChatMsg {
  role: 'user' | 'assistant' | 'tool' | 'approval' | 'todo' | 'ask'
  content: string
  toolName?: string
  status?: string
  thinking?: string
  // 编辑类工具的结构化 diff（由内核字段透传，非文本解析所得）
  diff?: string
  // 工具卡语义标签（内核产出）：title 主标签（文件名/命令首段/搜索词），op 动作类型
  // （read/write/edit/list/exec/search/git）。旧账本缺省 → 卡片回退工具名渲染
  title?: string
  op?: string
  // 任务清单数据（role='todo'）：todo 工具的全量快照，单卡原地更新
  todos?: TodoItem[]
  // 问答卡数据（role='ask'）：askId 用于答复回流；answered 区分待答/已答
  question?: string
  options?: string[]
  askId?: string
  answered?: boolean
  // 审批卡片数据（role='approval'）：id 用于回传答复；args 为原始 JSON 原样展示；
  // sessionTitle 是发起该审批的会话名（0.2.36 审计 R3：确认卡必须标明是哪个对话）
  approvalId?: string
  args?: string
  sessionTitle?: string
  // 驾驶舱数据（0.0.28，仅 browser 工具的终态卡携带）：截图相对路径（browser-shots
  // 根下正斜杠，面板经壳层 ReadBrowserShot 读图）+ 落地 URL + 控制台尾部。
  // undefined = 本卡不带该数据（running 卡/旧账本）；空串/空数组 = 明确携带的空值
  //（如"本次截图失败"）——面板按"取最新携带者"的规则派生驾驶舱视图。
  shot?: string
  url?: string
  console?: string[]
  // 工具调用配对 ID（0.0.06）：running 与终态事件同 ID——终态原地更新"执行中"卡
  callId?: string
  // 撤销元数据（0.0.07）：hasUndo 时卡片显示"恢复写入前"；旧全文不进前端，
  // 恢复走后端 RestoreToolWrite（比对哈希防覆盖用户改动）
  hasUndo?: boolean
  undoPath?: string
  undoNote?: string
  // 用户消息附件（0.0.10）：重放后仍显示图片/文件
  attachments?: { kind: string; name: string; mediaType?: string; dataUrl?: string; path?: string; inline?: string }[]
  at?: number
  term?: number
  streaming?: boolean
  error?: boolean
  // 本轮耗时（毫秒，terminal 时计算）：AI 工具标配的耗时反馈
  durationMs?: number
  // 会话内唯一 id：列表 key 与折叠态的稳定锚点（下标会在工具卡插入时整体错位）
  id?: string
  // 账本事件序号（第 6 批）：用户消息带它——「从这条消息重跑」的分叉锚点
  seq?: number
}

// 消息序号：入库时统一发 id——Replay/事件/本地推送都走这一处
let msgSeq = 0
function withId<T extends Omit<ChatMsg, 'id'>>(m: T): ChatMsg {
  return { ...m, id: `m-${++msgSeq}` }
}

// 浏览器驾驶舱视图（0.0.28）：面板展示的"模型正在看的页面"，由当前会话缓冲派生。
export interface BrowserVisual {
  shot: string // 最新截图相对路径（'' = 从未截到，或最近一次明确截图失败）
  url: string // 最近一次落地的页面 URL
  console: string[] // 最近一次随卡携带的控制台尾部（≤8 条）
  snapshot: string // 最近一次 open/snapshot/scroll 的元素快照正文（'' = 无）
  running: boolean // 最新 browser 卡是否执行中（面板显示"模型正在操作"）
}

// browserSnapshotText 取 browser 卡的元素快照正文（open/snapshot/scroll 的输出含
// [ref] 元素列表；click/fill/back/console/screenshot 不带）。动作判据：内核给
// browser 卡的 Title 以动作名开头（internal/platform/browsertool 的 Title 方案）。
const SNAPSHOT_ACTIONS = /^(open|snapshot|scroll)(\s|$)/
export function browserSnapshotText(m: ChatMsg): string {
  if (m.role !== 'tool' || m.toolName !== 'browser' || m.status === 'running') return ''
  if (!SNAPSHOT_ACTIONS.test((m.title || '').trim())) return ''
  return m.content || ''
}

// 待发送附件（0.0.10）：Composer 待发送区的单项。
// dataB64：剪贴板/临时文件内容；sourcePath：磁盘文件路径（二选一）。
export interface PendingAttachment {
  kind: 'image' | 'file'
  name: string
  mediaType: string
  size: number
  dataB64?: string
  sourcePath?: string
  inline: 'full' | 'path' | 'none'
}

// EndReason 与 core/llm 的枚举一一对应（经事件桥以 int 传输）。
export const END_REASON = { DONE: 1, ERROR: 2, CANCELLED: 3, IDLE_TIMEOUT: 4 } as const

// 常见网络错误的中文翻译：Windows 的 wsarecv/连接类错误原文对用户不友好
//（实机：直连海外中转被墙时用户看到一屏英文不知所措）。保留原始错误供诊断。
function humanizeNetError(raw: string): string {
  const rules: [RegExp, string][] = [
    [/wsarecv|wsasend|connection attempt failed|did not properly respond/i, '网络连接失败：上游服务器无响应或连接被阻断（若上游在海外，请确认已配置全局代理）'],
    [/connection refused/i, '连接被拒绝：上游服务未开放该端口'],
    [/no such host|lookup/i, '域名解析失败：请检查网络或渠道地址'],
    [/i\/o timeout|context deadline|timeout/i, '连接超时'],
    [/proxyconnect|proxy error|proxy handshake/i, '代理连接失败：请检查全局代理设置与代理软件'],
    [/certificate|tls:|x509/i, 'TLS/证书异常：上游或代理的证书有问题'],
  ]
  for (const [re, msg] of rules) {
    if (re.test(raw)) return `${msg}｜原始错误：${raw}`
  }
  return raw
}

// 终态的人类可读标签：UI 围绕"可区分的三终态"设计（docs/CONTRACTS.md）。
function terminalLabel(reason: number, raw: string): string {
  switch (reason) {
    case END_REASON.CANCELLED:
      return '⚠ 已取消'
    case END_REASON.IDLE_TIMEOUT:
      return '⚠ 响应超时：上游长时间无响应，可重试'
    default:
      return `⚠ 出错了：${raw ? humanizeNetError(raw) : '未知错误'}`
  }
}

// 会话 ID 计数器：同一毫秒内也可能连续新建两个会话，必须保证 ID 唯一——
// ID 就是账本文件名，重复即两个会话串写同一份账本（测试曾当场抓到：同一秒内
// 新建的 A/B 两会话拿到同一个 ID，消息互相混进对方的账本）。
let sessionSeq = 0

// 生成本地时间会话 ID（草稿首聊落地时才领号）。为什么不用 Date.toISOString：其 UTC 时刻与本地时间观感不一致。
function newSessionId(): string {
  const d = new Date()
  const p = (n: number, w = 2) => String(n).padStart(w, '0')
  return `s-${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}${p(d.getMilliseconds(), 3)}-${++sessionSeq}`
}

// 强制工具（第 7 批）：本轮"模型开口前必须先调用"的工具——输入框里指定的技能或
// MCP 工具。参数由前端给定（模型改不了服务器名/工具名），账本里仍是普通工具调用/结果。
export interface ForcedToolDTO {
  name: 'skill' | 'mcp'
  arguments: Record<string, unknown>
}

// 排队消息（0.0.11 带附件）：回合进行中提交的那条，终态后由 onTerminal 续发。
// 附件必须随文字一起排队——只排文字时用户贴的截图/拖入的文件会留在待发送区，
// 续发的那条消息丢附件（用户以为发了，模型没收到）。
export interface QueuedMessage {
  id: number
  text: string
  atts: PendingAttachment[]
  // 强制工具随队列走（第 7 批）：与附件同理——不跟着走的话，排队那条会静默丢掉
  // "先调这个技能/MCP"的指定。
  forced?: ForcedToolDTO
}

// 单个会话的运行态（0.2.25 多会话）：消息缓冲 + 运行标志 + 输入队列 + 本轮计时。
// 为什么按会话各一份：回合进行中也允许切到别的会话继续聊——流式事件必须各归各位
// （后台会话的事件照常入它自己的缓冲，不再被丢弃），切回来时原地接着看。
interface Conversation {
  messages: ChatMsg[]
  running: boolean
  stopping: boolean
  queue: QueuedMessage[]
  turnStartedAt: number // 0 = 无进行中轮次
}

function newConversation(): Conversation {
  return { messages: [], running: false, stopping: false, queue: [], turnStartedAt: 0 }
}

export const useChatStore = defineStore('chat', () => {
  // 当前查看的会话；'' = 草稿。切换不再受"是否有会话在跑"限制（0.2.25）。
  const sessionId = ref('')
  const sessions = ref<string[]>([])
  const error = ref('')

  // 会话摘要（ID + 标题）：标题为空时侧栏回退显示 ID
  const summaries = ref<SessionSummaryDTO[]>([])

  // 按会话隔离的运行态。key = 会话 ID（'' = 草稿）。
  const convos = reactive(new Map<string, Conversation>())

  // convoOf 是**只读**取用：渲染/求值期绝不创建条目——侧栏渲染会对每条会话调用
  // isRunning，若在此建条目，所有会话都"看起来有缓冲"，selectSession 的
  // Replay 条件被打假，历史会话永远打不开（0.2.26 实机回归）。写路径一律用 ensureConvo。
  function convoOf(id: string): Conversation | undefined {
    return convos.get(id)
  }

  // ensureConvo 是显式写路径（发送 / 事件回调 / 选中会话）：确保条目存在。
  //
  // 关键（0.2.31 实机根因）：首次创建时必须 set 之后**重新 get** 再返回——
  // reactive(Map).get 返回的是 value 的响应式代理，而 set 进去的是原始对象；
  // 直接返回原始对象会让"首次创建后立即写入"（冷启动 Replay 赋值历史）绕过
  // 响应式系统：computed 不触发，视图停在空态——用户看到的就是"打开软件直接
  // 对话没显示，切换一下会话（触发重算）才出来"。
  function ensureConvo(id: string): Conversation {
    const existing = convos.get(id)
    if (existing) return existing
    convos.set(id, newConversation())
    const c = convos.get(id)
    if (!c) {
      // 理论不可达（刚 set）；兜底成不崩溃的空对象，绝不返回 undefined
      return newConversation()
    }
    return c
  }

  // 空缓冲占位：只读路径的返回值（模板读 messages.length 不能拿到 undefined；
  // 常量共享且绝不被写入）
  const EMPTY_MESSAGES: ChatMsg[] = []
  const EMPTY_QUEUE: QueuedMessage[] = []

  // 当前视图 = 当前会话的运行态。读写都收敛到这里，组件无感（仍是 store.messages/running/queue）。
  const messages = computed(() => convoOf(sessionId.value)?.messages ?? EMPTY_MESSAGES)
  const running = computed({
    get: () => convoOf(sessionId.value)?.running ?? false,
    set: (v: boolean) => {
      ensureConvo(sessionId.value).running = v
    },
  })
  const stopping = computed({
    get: () => convoOf(sessionId.value)?.stopping ?? false,
    set: (v: boolean) => {
      ensureConvo(sessionId.value).stopping = v
    },
  })
  const queue = computed({
    get: () => convoOf(sessionId.value)?.queue ?? EMPTY_QUEUE,
    set: (v: QueuedMessage[]) => {
      ensureConvo(sessionId.value).queue = v
    },
  })

  // 切换会话的历史载入态（Replay 期间渲染"正在载入历史…"，避免闪一下空态）
  const loadingSession = ref(false)

  // 某个会话是否正在跑（侧栏指示灯与"删除运行中会话"的判据；与当前视图无关）
  function isRunning(id: string): boolean {
    return convoOf(id)?.running ?? false
  }

  // 是否有任意会话在跑（顶栏状态灯用）：后台会话也算运行中，
  // 否则切到别的会话看时顶栏显示"空闲"，用户会以为后台那轮停了。
  const anyRunning = computed(() => {
    for (const c of convos.values()) {
      if (c.running) return true
    }
    return false
  })

  // 某个会话有几项等待用户答复的请求（未决审批 + 未答问答）——后台会话卡在
  // 等答复时用户必须能被提示到（否则整轮无声挂起，用户不知道要回去处理）
  function pendingOf(id: string): number {
    const c = convoOf(id)
    if (!c) return 0
    let n = 0
    for (const m of c.messages) {
      if (m.role === 'approval' && !m.status) n++
      if (m.role === 'ask' && !m.answered) n++
    }
    return n
  }

  const anyPending = computed(() => {
    let n = 0
    for (const id of convos.keys()) n += pendingOf(id)
    return n
  })

  // 正在运行的会话（含当前视图之外的后台轮次）——跳转目标的数据源
  const runningSessions = computed(() => {
    const out: string[] = []
    for (const [id, c] of convos) {
      if (c.running) out.push(id)
    }
    return out
  })

  // 状态灯/侧栏的跳转目标：优先"有等待答复请求"的会话，其次任意其他运行中会话。
  // 空串 = 没有值得跳转的目标（当前视图就是唯一的忙碌会话）。
  const busyTarget = computed(() => {
    const pend = summaries.value.find((s) => s.id !== sessionId.value && pendingOf(s.id) > 0)
    if (pend) return pend.id
    return runningSessions.value.find((id) => id !== sessionId.value) ?? ''
  })

  async function loadSessions() {
    try {
      summaries.value = (await bridge().app.ListSessionSummaries()) ?? []
      sessions.value = summaries.value.map((s) => s.id)
    } catch (e) {
      // 列表读取失败必须可见（静默会让侧栏停在旧数据上，用户以为会话丢了）
      error.value = `读取会话列表失败：${errText(e)}`
    }
  }

  function titleOf(id: string): string {
    return summaries.value.find((s) => s.id === id)?.title || id
  }

  async function renameSession(id: string, title: string) {
    error.value = ''
    try {
      await bridge().app.RenameSession(id, title)
      await loadSessions()
    } catch (e) {
      error.value = errText(e)
    }
  }

  // 尾屏加载（0.3 长会话减重）：切会话只取投影的最后一屏，向上滚动再补更早的
  //（后端 ReplayTail / ReplayOlder 分页；投影总数 total 用来判断还有没有更早的）。
  const REPLAY_TAIL_LIMIT = 40

  // mapHistory 把后端投影映射为 UI 消息（尾屏与向上翻页共用一份映射）。
  function mapHistory(history: ChatMessageDTO[]): ChatMsg[] {
    return history.map((m) => {
      // 任务卡：Content 为 items JSON（Replay 投影只保留最新快照）
      if (m.role === 'todo') {
        let todos: TodoItem[] = []
        try {
          todos = JSON.parse(m.content) as TodoItem[]
        } catch {
          todos = [] // 旧数据/损坏数据容错：空清单，不阻塞恢复
        }
        return withId({ role: 'todo', content: '', todos })
      }
      // 问答卡：Content 为用户答复；问题/选项由账本 tool_call 配对投影，恢复即已答态
      if (m.role === 'ask') {
        return withId({
          role: 'ask',
          content: m.content,
          question: m.question,
          options: m.options,
          answered: true,
        })
      }
      return withId({
        role: m.role as ChatMsg['role'],
        content: m.content,
        toolName: m.toolName,
        status: m.status,
        thinking: m.thinking,
        title: m.title,
        op: m.op,
        diff: m.diff,
        callId: m.callId,
        hasUndo: m.hasUndo,
        undoPath: m.undoPath,
        undoNote: m.undoNote,
        // 驾驶舱数据同构投影（0.0.28）：重启后驾驶舱仍显示"当时的画面"
        shot: m.shot,
        url: m.url,
        console: m.console,
        attachments: m.attachments,
        seq: m.seq,
      })
    })
  }

  // 恢复写入前（0.0.07）：走后端（比对哈希防覆盖用户改动）；成功后把"已恢复"
  // 卡片追加到当前视图（后端同时落账本，重启后 Replay 仍显示）。
  async function restoreWrite(callID: string, undoPath?: string) {
    error.value = ''
    try {
      const note = await bridge().app.RestoreToolWrite(sessionId.value, callID)
      const c = ensureConvo(sessionId.value)
      c.messages.push(
        withId({
          role: 'tool',
          content: note,
          toolName: 'fs',
          status: 'success',
          op: 'edit',
          title: undoPath,
          at: Date.now(),
        }),
      )
    } catch (e) {
      error.value = errText(e)
    }
  }

  // overlapCount 返回 hist 尾部与 live 前缀的最大逐条重叠数（role+content 比对）。
  // 用于 Replay 与本地消息的合并去重：同一份后端账本投影与本地 push 的同一条
  // 消息（role+content 相同）只保留一份。空 content 的"思考占位"不会与历史里的
  // 富内容误判相同——最多匹配到 user 消息那一条（正确：它已在历史里）。
  function overlapCount(hist: ChatMsg[], live: ChatMsg[]): number {
    const max = Math.min(hist.length, live.length)
    for (let k = max; k > 0; k--) {
      let same = true
      for (let i = 0; i < k; i++) {
        const h = hist[hist.length - k + i]
        const l = live[i]
        if (h.role !== l.role || h.content !== l.content) {
          same = false
          break
        }
      }
      if (same) return k
    }
    return 0
  }

  async function selectSession(id: string) {
    // 切换会话只改视图，绝不碰工作区根（0.2.37 用户反馈）：已有会话的工具根由
    // 后端账本首个 workspace 事件固定，切到这里改顶栏工作区既影响不到当前对话，
    // 反而会改掉"还没发出去的下一场新对话"的根——点开未分组旧会话清空工作区
    // （新对话变纯对话丢文件工具）、点开 B 项目会话把新对话写进 B。新对话的根
    // 只在用户显式选择工作区、或在草稿上发送时确定。
    sessionId.value = id
    contextInfo.value = null // 上下文读数属于具体会话：切换后显示新会话的最近读数（无则不显示）
    olderFrom.value = 0 // 新视图的翻页锚点复位：是否还有更早的历史由本次尾屏决定
    if (!convos.has(id)) {
      loadingSession.value = true
      const c = ensureConvo(id)
      try {
        // 尾屏优先（0.3）：只取最后一屏，长会话不再等全量投影传完才见首屏；
        // 更早的历史等用户向上滚动时经 loadOlder 分页补齐（DOM 窗口照常工作）。
        const page = await bridge().app.ReplayTail(id, REPLAY_TAIL_LIMIT)
        const hist = mapHistory(page?.messages ?? [])
        olderFrom.value = Math.max(0, (page?.total ?? hist.length) - hist.length)
        // Replay 窗口（IPC 往返）内用户可能已经发了消息（冷启动直接对话，0.2.30
        // 实机：打开软件就发，随后"什么都没显示"）。此前整体覆盖 `c.messages = hist`
        // 会把窗口内刚 push 的消息丢掉——改为历史前置、本地保留，重叠前缀去重。
        if (c.messages.length === 0) {
          c.messages = hist
        } else {
          const skip = overlapCount(hist, c.messages)
          c.messages = [...hist, ...c.messages.slice(skip)]
        }
      } catch (e) {
        // 历史载入失败必须可见（此前是静默空白：用户以为会话内容丢了）
        error.value = `载入会话历史失败：${errText(e)}`
      } finally {
        loadingSession.value = false
      }
    }
  }

  // ---- 向上翻页（0.3 尾屏优先的另一半）----
  // olderFrom 是"已载入的投影条数从末尾往前数的位置"（0 = 前面没有了）。
  // 新事件只会追加在投影尾部，前缀下标稳定——翻页锚点不受后台轮次追加影响。
  const olderFrom = ref(0)
  const olderAvailable = computed(() => olderFrom.value > 0)
  const loadingOlder = ref(false)

  // loadOlder 取更早的一页并前置进缓冲（保持账本投影顺序；本地 live 消息只在尾部追加，
  // 前插不会与本地消息交错）。
  async function loadOlder() {
    const id = sessionId.value
    if (!id || loadingOlder.value || olderFrom.value <= 0) return
    loadingOlder.value = true
    try {
      const page = await bridge().app.ReplayOlder(id, olderFrom.value, REPLAY_TAIL_LIMIT)
      const older = mapHistory(page?.messages ?? [])
      olderFrom.value = Math.max(0, page?.from ?? 0)
      if (older.length) {
        const c = ensureConvo(id)
        c.messages = [...older, ...c.messages]
      }
    } catch (e) {
      // 翻页失败必须可见（静默会让用户以为"前面没有了"）
      error.value = `载入更早的消息失败：${errText(e)}`
    } finally {
      loadingOlder.value = false
    }
  }

  // 新会话 = 草稿：不生成 ID、不进侧栏（侧栏只镜像事件账本）。
  // 首条消息发出时才在 send() 领 ID 落账本，经 loadSessions 进入侧栏。
  // 多会话（0.2.25）：进行中的轮次各自继续，新建/切换不再被"有会话在跑"挡住。
  async function newSession() {
    sessionId.value = '' // '' 即草稿态
    ensureConvo('').messages = []
  }

  // 首聊即时入列：新会话发出第一条消息的瞬间就出现在侧栏并归属当前工作区，
  // 不等回合结束（0.2.10 反馈：长任务跑完前侧栏看不到它，空间分组也一样迟到）。
  // 这是本地待定摘要：标题/时间由回合结束后的 loadSessions 用账本真实数据校正；
  // 自动命名逻辑不受影响（合成条目 title 为空 → 仍判为"未命名"→ 走自动命名）。
  function announcePending() {
    if (summaries.value.some((s) => s.id === sessionId.value)) return
    const ws = useWorkspaceStore()
    summaries.value.unshift({
      id: sessionId.value,
      title: '',
      pinned: false,
      workspace: ws.path || undefined,
      lastActiveMs: Date.now(),
    })
  }

  // 发送到"当前正在看的会话"（用户动作入口）。
  // 0.0.10：atts 非空时走带附件的 IPC；throwOnError 由 Composer 传入（失败保留待发送区）。
  function send(
    text: string,
    atts?: PendingAttachment[],
    opts?: { throwOnError?: boolean; forced?: ForcedToolDTO },
  ) {
    if (!sessionId.value) {
      sessionId.value = newSessionId() // 草稿首聊：此刻才领 ID，由后端 Send 落账本
      announcePending()
    }
    return sendTo(sessionId.value, text, atts, opts)
  }

  // 发送到指定会话（多会话的核心：后台会话的排队续发也走这里，绝不发进当前视图）。
  // 0.0.10：带附件且 opts.throwOnError 时发送失败上抛——Composer 据此保留
  // 文字与附件允许重试；默认路径不抛（错误气泡已可见，调用方多为 fire-and-forget）。
  // 本地回显：user 消息立刻带附件形态（图片直接用 base64 展示，无需等重放）。
  async function sendTo(
    id: string,
    text: string,
    atts?: PendingAttachment[],
    opts?: { throwOnError?: boolean; forced?: ForcedToolDTO },
  ) {
    const c = ensureConvo(id)
    const localAtts = atts?.map((a) => ({
      kind: a.kind,
      name: a.name,
      mediaType: a.mediaType,
      dataUrl: a.dataB64 ? `data:${a.mediaType};base64,${a.dataB64}` : undefined,
      path: a.sourcePath,
      inline: a.inline,
    }))
    c.messages.push(withId({ role: 'user', content: text, attachments: localAtts, at: Date.now() }))
    // 任务清单生命周期（0.0.11）：新用户消息 = 新任务开始——上一份清单立即退场，
    // 不再挂着旧的（模型这一轮不调 todo 工具时旧清单会永远残留）。新一轮的
    // onTodo 会新建卡；账本里历史快照不受影响（重放由"全完成退场"规则兜住）。
    for (let i = c.messages.length - 1; i >= 0; i--) {
      if (c.messages[i].role === 'todo') {
        c.messages.splice(i, 1)
      }
    }
    // "正在思考"占位（0.2.28）：慢中转/上游挂起时用户立即看到反馈，而不是
    // "消息发出去了，什么都没发生"。onChunk 复用这段（inFlightAssistant 按
    // streaming 段查找）；零块 DONE 终态时移除空占位，不留空气泡。
    c.messages.push(withId({ role: 'assistant', content: '', streaming: true, at: Date.now() }))
    c.running = true
    c.stopping = false
    c.turnStartedAt = Date.now()
    try {
      if ((atts && atts.length) || opts?.forced) {
        // 指定了技能/MCP 时也走带附件那条：本轮要先强制调用一次工具（第 7 批）
        await bridge().app.SendWithAttachments(
          id,
          text,
          JSON.stringify(atts ?? []),
          opts?.forced ? JSON.stringify(opts.forced) : '',
        )
      } else {
        // Send 在轮次结束（终态事件已发出）后才 resolve；前置错误走 IPC error
        await bridge().app.Send(id, text)
      }
    } catch (e) {
      const ast = inFlightAssistant(c)
      if (ast) {
        ast.streaming = false
        ast.error = true
        ast.content = `⚠ 出错了：${String(e)}`
      } else {
        // 任何块都未到达就失败（如渠道未配置）：错误必须可见，不静默
        c.messages.push(
          withId({ role: 'assistant', content: `⚠ 出错了：${String(e)}`, error: true, at: Date.now() }),
        )
      }
      c.running = false
      c.stopping = false
      if (opts?.throwOnError) throw e // Composer 附件路径：保留文字与附件允许重试
    }
  }

  // 本轮占位助手：工具卡会插在它前面，不能用 messages.at(-1)。
  function inFlightAssistant(c: Conversation): ChatMsg | undefined {
    return c.messages.find((m) => m.role === 'assistant' && m.streaming)
  }

  // turnHadAttachments 判断"占位所在这一轮"是否带了附件：从占位往前找最近的一条
  // 用户消息（工具卡、任务清单等会插在中间，跳过）。附件轮的空回答要留一条可见说明
  // （0.0.25）——删掉占位就表现为"发出去就没了"，与"附件根本没送达"分不开。
  function turnHadAttachments(c: Conversation, idx: number): boolean {
    for (let i = idx - 1; i >= 0; i--) {
      const m = c.messages[i]
      if (m.role === 'user') return !!m.attachments?.length
    }
    return false
  }

  // 审批卡片：内核要"问"时插入一张带允许/拒绝按钮的卡片（ADR-0007）。
  // sessionID 必填：缺标识的卡片宁可丢弃并报错，也绝不插进当前视图（串会话）。
  function onApproval(p: {
    id: string
    sessionID: string
    sessionTitle?: string
    toolName: string
    arguments: string
  }) {
    if (!p.sessionID) {
      error.value = '收到缺少会话标识的审批事件（已丢弃，避免串会话）'
      return
    }
    ensureConvo(p.sessionID).messages.push(
      withId({
        role: 'approval',
        content: p.toolName,
        toolName: p.toolName,
        approvalId: p.id,
        args: p.arguments,
        sessionTitle: p.sessionTitle,
        at: Date.now(),
      }),
    )
  }

  // 在全部会话缓冲里找一张卡（答复回流时卡片可能在后台会话里）
  function findCard(pred: (m: ChatMsg) => boolean): ChatMsg | undefined {
    for (const c of convos.values()) {
      const card = c.messages.find(pred)
      if (card) return card
    }
    return undefined
  }

  // 提交答复：失败必须可见（例如"已处理"），绝不静默
  async function resolveApproval(id: string, approved: boolean, reason = '') {
    error.value = ''
    try {
      await bridge().app.ResolveApproval(id, approved, reason)
    } catch (e) {
      error.value = errText(e)
      return
    }
    const card = findCard((m) => m.approvalId === id)
    if (card) card.status = approved ? 'approved' : 'denied'
  }

  // 置顶/取消置顶：失败可见（error 位），成功后刷新摘要（分区与排序随之变化）
  async function pinSession(id: string, pinned: boolean) {
    error.value = ''
    try {
      await bridge().app.PinSession(id, pinned)
      await loadSessions()
    } catch (e) {
      error.value = errText(e)
    }
  }

  // 返回是否成功：失败时错误落 error 位（可见），调用方（顶栏开关）据此回滚
  // 乐观状态——不回滚的话开关显示与实际策略相反。
  async function setApprovalPolicy(tools: string[]): Promise<boolean> {
    error.value = ''
    try {
      await bridge().app.SetApprovalPolicy(tools)
      return true
    } catch (e) {
      error.value = errText(e)
      return false
    }
  }

  async function loadApprovalPolicy(): Promise<string[]> {
    try {
      return (await bridge().app.ApprovalPolicy()) ?? []
    } catch (e) {
      error.value = errText(e)
      return []
    }
  }

  // 事件桥回调（App.vue onMounted 绑定）。多会话（0.2.25）：全部按 sessionID
  // 路由进各自的缓冲——后台会话的事件照常入账，绝不再因"不是当前视图"被丢弃。
  function onChunk(p: { sessionID: string; delta: string; thinking: string }) {
    const c = ensureConvo(p.sessionID)
    let ast = inFlightAssistant(c)
    if (!ast) {
      // ReAct 新轮次：无进行中助手则新开一段——工具卡之后的增量落进下一段
      ast = withId({ role: 'assistant', content: '', streaming: true, at: Date.now() })
      c.messages.push(ast)
    }
    ast.content += p.delta
    if (p.thinking) ast.thinking = (ast.thinking || '') + p.thinking
  }

  function onTool(p: ChatToolEventDTO) {
    const c = ensureConvo(p.sessionID)
    if (p.name === 'todo') return // 任务清单由 onTodo/FloatingTodo 承载，不重复出工具卡
    if (p.name === 'ask_user') return // 问答卡由 onAsk/AskCard 承载，答案已在卡上
    // running 事件（0.0.06）：先出"执行中"卡（默认展开），终态事件按 callId
    // 原地更新同一张卡——卡随事件增长，而不是插两张卡。
    // 0.0.07：同一 CallID 的后续 running 事件（shell 过程推送）**更新已捕获
    // 输出**，不再被"重复事件"守卫吞掉——卡片里看得见命令在吐什么。
    if (p.status === 'running' && p.callID) {
      const existing = c.messages.find((m) => m.role === 'tool' && m.callId === p.callID)
      if (existing) {
        if (p.content || p.summary) existing.content = p.content || p.summary
        if (p.title && !existing.title) existing.title = p.title
        if (p.op && !existing.op) existing.op = p.op
        return
      }
      const ast = inFlightAssistant(c)
      if (ast) ast.streaming = false
      const card = withId({
        role: 'tool' as const,
        content: p.content || p.summary || '执行中…',
        toolName: p.name,
        status: 'running',
        title: p.title,
        op: p.op,
        callId: p.callID,
        streaming: true,
        at: Date.now(),
      })
      const i = ast ? c.messages.indexOf(ast) + 1 : c.messages.length
      c.messages.splice(i, 0, card)
      autoOpenPanelsFor(p)
      return
    }
    // 终态事件：优先更新同 callId 的"执行中"卡（原地生长），没有则新建
    //（兼容旧后端/重放：终态事件总是独立成卡）
    let target: ChatMsg | undefined
    if (p.callID) {
      target = c.messages.find((m) => m.role === 'tool' && m.callId === p.callID && m.status === 'running')
    }
    if (target) {
      target.status = p.status
      target.content = p.content || p.summary || target.content
      if (p.diff !== undefined) target.diff = p.diff
      if (p.title) target.title = p.title
      if (p.op) target.op = p.op
      // 驾驶舱字段（0.0.28）：undefined = 本卡不带（running 卡/非 browser），保留原值；
      // 空串/空数组 = 明确携带的空值，照实覆盖
      if (p.shot !== undefined) target.shot = p.shot
      if (p.url !== undefined) target.url = p.url
      if (p.console) target.console = p.console
      target.hasUndo = !!p.hasUndo
      if (p.undoPath) target.undoPath = p.undoPath
      if (p.undoNote) target.undoNote = p.undoNote
      target.streaming = false
      autoOpenPanelsFor(p)
      return
    }
    // 封存当前段：ReAct 叙事顺序 = 本轮思考/文本 → 工具卡 → 下一段（onChunk 再开新段）
    const ast = inFlightAssistant(c)
    if (ast) ast.streaming = false
    const card = withId({
      role: 'tool' as const,
      content: p.content || p.summary,
      toolName: p.name,
      status: p.status,
      diff: p.diff,
      title: p.title,
      op: p.op,
      callId: p.callID,
      hasUndo: !!p.hasUndo,
      undoPath: p.undoPath,
      undoNote: p.undoNote,
      shot: p.shot,
      url: p.url,
      console: p.console,
      at: Date.now(),
    })
    const i = ast ? c.messages.indexOf(ast) + 1 : c.messages.length
    c.messages.splice(i, 0, card)
    autoOpenPanelsFor(p)
  }

  // 任务清单：单卡原地更新（同会话只保留一张，位置保留首次出现处）
  function onTodo(p: { sessionID: string; items: TodoItem[] }) {
    const c = ensureConvo(p.sessionID)
    const existing = c.messages.find((m) => m.role === 'todo')
    if (existing) {
      existing.todos = p.items
      return
    }
    const ast = inFlightAssistant(c)
    const card = withId({ role: 'todo', content: '', todos: p.items, at: Date.now() })
    const i = ast ? c.messages.indexOf(ast) : c.messages.length
    c.messages.splice(i, 0, card)
  }

  // 问答卡（ask_user）：插入待答卡片并封存当前段——叙事顺序 = 本轮文本 → 问答卡 → 回复。
  // sessionID 必填（缺标识丢弃并报错，绝不插进当前视图）
  function onAsk(p: { id: string; sessionID: string; question: string; options?: string[] }) {
    if (!p.sessionID) {
      error.value = '收到缺少会话标识的问答事件（已丢弃，避免串会话）'
      return
    }
    const c = ensureConvo(p.sessionID)
    const ast = inFlightAssistant(c)
    if (ast) ast.streaming = false
    c.messages.push(
      withId({
        role: 'ask',
        content: '',
        question: p.question,
        options: p.options ?? [],
        askId: p.id,
        at: Date.now(),
      }),
    )
  }

  // 提交答复：失败必须可见（例如"已处理"），成功后卡片转已答态展示所选答案
  async function resolveAsk(id: string, answer: string) {
    error.value = ''
    try {
      await bridge().app.ResolveAsk(id, answer)
    } catch (e) {
      error.value = errText(e)
      return
    }
    const card = findCard((m) => m.askId === id)
    if (card) {
      card.answered = true
      card.content = answer
    }
  }

  // 油表（0.0.09）：本轮最后一次上游 usage 的 prompt token（= 当前上下文大小
  // 的直接读数）。没有上下文长度配置，绝不显示编造的百分比。
  const promptTokens = ref(0)
  function onUsage(p: { sessionID: string; prompt: number; completion: number; total: number }) {
    if (p.sessionID !== sessionId.value) return
    promptTokens.value = p.prompt
  }

  // 上下文治理读数（第 2 批 + 阶段 5-2 + 0.3 细分）：本轮预算（含"渠道未声明→默认值"
  // 的来源标记）与本轮为压回预算执行的折叠，**按类别分开记**——油表要写清"折了什么"
  //（旧工具输出 / 图片 / 重复读 / 旧回复），禁止静默丢历史。folded 为四类合计。
  const contextInfo = ref<{
    estimatedTokens: number
    budgetTokens: number
    budgetDefault: boolean
    folded: number
    foldedImages: number
    foldedTools: number
    foldedReads: number
    foldedBodies: number
    dropped: boolean
  } | null>(null)
  function onContext(p: {
    sessionID: string
    estimatedTokens: number
    budgetTokens: number
    budgetDefault?: boolean
    foldedImages: number
    foldedTools: number
    foldedReads: number
    foldedBodies?: number
    dropped: boolean
  }) {
    if (p.sessionID !== sessionId.value) return
    const foldedBodies = p.foldedBodies ?? 0
    contextInfo.value = {
      estimatedTokens: p.estimatedTokens,
      budgetTokens: p.budgetTokens,
      budgetDefault: p.budgetDefault === true,
      folded: p.foldedImages + p.foldedTools + p.foldedReads + foldedBodies,
      foldedImages: p.foldedImages,
      foldedTools: p.foldedTools,
      foldedReads: p.foldedReads,
      foldedBodies,
      dropped: p.dropped,
    }
  }

  // 撤回本轮（第 6 批）：按轮次检查点恢复本轮改过的文件；撤不回的由调用方展示
  //（绝不静默）。失败原样上抛（调用方 toast）。
  async function revertRound(): Promise<RevertResultDTO> {
    error.value = ''
    try {
      const res = await bridge().app.RevertRound(sessionId.value)
      return res ?? { round: 0, restored: [], skipped: [] }
    } catch (e) {
      error.value = errText(e)
      throw e
    }
  }

  // 从这条用户消息重跑（第 6 批）：后端撤回其后文件改动并分叉账本（丢弃该消息及其后
  // 的旧历史，旧行不改写）；本地同步裁掉该消息及其后的缓冲，再按原文重发。
  async function rerunFrom(userSeq: number): Promise<RerunResultDTO> {
    error.value = ''
    try {
      const res = await bridge().app.RerunFrom(sessionId.value, userSeq)
      if (!res) throw new Error('重跑失败：内核未返回结果')
      const c = ensureConvo(sessionId.value)
      const idx = c.messages.findIndex((m) => m.role === 'user' && m.seq === userSeq)
      if (idx >= 0) c.messages.splice(idx)
      return res
    } catch (e) {
      error.value = errText(e)
      throw e
    }
  }

  // 文件详情面板（第 3 批）：当前在右侧专看的文件路径（'' = 关闭）。
  // 打开来源：本轮变更的文件行 / 工具卡文件名——diff 是主视图，右侧专区显示该文件的
  // 全部改动（逐次 diff + 统计 + 撤销），不再把 diff 挤在消息流里。
  const fileDetailPath = ref('')
  function openFileDetail(path: string) {
    const p = path.trim()
    if (!p) return
    fileDetailPath.value = p
    rightPanelTab.value = 'file' // 打开哪个 tab 就激活哪个（与 openBrowserPanel 同一不变式）
  }
  function closeFileDetail() {
    fileDetailPath.value = ''
  }

  // ---- 浏览器驾驶舱（0.0.28）----
  // 面板数据 = 当前会话缓冲的派生（browserVisual）：不加第二份状态——browser 工具卡
  // 本身就是驾驶舱数据，切会话自动跟随、Replay 自动恢复、running 卡原地生长都免费拿到。
  // 面板开关与激活 tab 是应用级视图状态（不属于任何会话）；文件 tab 的开态即 fileDetailPath。

  const rightPanelTab = ref<'file' | 'browser' | 'tree' | 'tasks'>('file')
  const browserOpen = ref(false)
  function openBrowserPanel() {
    browserOpen.value = true
    rightPanelTab.value = 'browser'
  }
  function closeBrowserPanel() {
    browserOpen.value = false
  }

  // 点链接 → 右侧驾驶舱打开（0.0.29）：对话输出里的网址绝不允许把应用窗口本身
  // 导航走（WebView 没有地址栏和后退，用户会被困在那个页面里出不来）。
  // 与模型共用会话浏览器 tab（所见即所控）；结果合成一张与模型工具卡同构的本地卡
  // 入会话缓冲——面板数据源保持唯一（browserVisual 派生），不为用户浏览单开状态。
  // 本地卡不落账本：浏览行为不是对话回合，切走再回来退回模型最近一张卡，是自觉取舍。
  async function openLinkInBrowser(url: string) {
    const sid = sessionId.value
    try {
      const view = await bridge().app.BrowserNavigate(sid, url)
      const c = ensureConvo(sid)
      c.messages.push(
        withId({
          role: 'tool' as const,
          content: view.output,
          toolName: 'browser',
          status: 'success' as const,
          title: view.title || 'open',
          op: 'exec',
          shot: view.shot,
          url: view.url,
          console: view.console,
          at: Date.now(),
        }),
      )
      openBrowserPanel()
    } catch {
      // 会话浏览器不可用（后端过旧/构造失败）：退回系统浏览器——链接必须到达，
      // 但宁可交给系统浏览器也不占用对话窗口
      openExternal(url)
    }
  }

  // ---- 目录树（右栏「目录」tab）----
  // 开态自持的应用级视图状态（不属于任何会话，与 browserOpen 同款）；树数据由
  // FileTreePanel 经 ListWorkspaceDir 逐层拉取，store 只管开合与激活。
  const treeOpen = ref(false)
  function openTreePanel() {
    treeOpen.value = true
    rightPanelTab.value = 'tree' // 打开哪个 tab 就激活哪个（与 openBrowserPanel 同一不变式）
  }
  function closeTreePanel() {
    treeOpen.value = false // 只关自己：注册表缩回后由容器回退第一项激活
  }

  // ---- 后台任务（右栏「任务」tab）----
  // 数据源 = shell 工具内存任务表（后端 BgTasksSnapshot 按会话快照），面板轮询拉取；
  // bg_start 卡到达时自动打开（openTasksFor），让"模型启动了后台进程"被看见。
  const tasksOpen = ref(false)
  function openTasksPanel() {
    tasksOpen.value = true
    rightPanelTab.value = 'tasks'
  }
  function closeTasksPanel() {
    tasksOpen.value = false
  }

  // openCockpitFor：browser 工具卡到达（当前会话）→ 自动打开驾驶舱并切到浏览器 tab。
  // 后台会话的浏览器事件照常入它自己的缓冲，但绝不抢当前视图（与审批/问答同纪律）。
  function openCockpitFor(p: ChatToolEventDTO) {
    if (p.name !== 'browser' || p.sessionID !== sessionId.value) return
    openBrowserPanel()
  }

  // openTasksFor：shell bg_start 卡（内核输出 JSON 带 task_id+pid，见 shelltool
  // bg.start 的返回契约）到达（当前会话）→ 自动打开任务面板。后台会话的卡不抢
  // 当前视图；running 过程推送与 run 的普通文本输出（非 JSON / 不带 pid）都不算。
  function openTasksFor(p: ChatToolEventDTO) {
    if (p.name !== 'shell' || p.status === 'running') return
    try {
      const payload = JSON.parse(p.content || '{}') as { task_id?: unknown; pid?: unknown }
      if (typeof payload.task_id === 'string' && typeof payload.pid === 'number' && p.sessionID === sessionId.value) {
        openTasksPanel()
      }
    } catch {
      // 非 JSON 输出：普通 run 卡，不打扰
    }
  }

  // autoOpenPanelsFor：每张工具卡的落卡路径只调一次的面板联动入口（驾驶舱 + 任务）。
  function autoOpenPanelsFor(p: ChatToolEventDTO) {
    openCockpitFor(p)
    openTasksFor(p)
  }

  // 驾驶舱各字段取"最新携带者"的值（从新往旧扫）：undefined = 本卡不带该数据
  //（running 卡 / error 路径 / 旧账本），回退更早的卡；空串/空数组 = 卡明确携带的
  // 空值（如"本次截图失败"），照实显示、不回退旧图——面板绝不拿旧画面冒充当前。
  const browserVisual = computed<BrowserVisual>(() => {
    const ms = messages.value
    let shot: string | undefined
    let url: string | undefined
    let cons: string[] | undefined
    let snapshot = ''
    let running = false
    let seenNewest = false
    for (let i = ms.length - 1; i >= 0; i--) {
      const m = ms[i]
      if (m.role !== 'tool' || m.toolName !== 'browser') continue
      if (!seenNewest) {
        seenNewest = true
        // 同一会话的浏览器动作天然串行：最新一张 browser 卡决定"执行中"
        running = m.status === 'running'
      }
      if (m.status === 'running') continue // running 卡不带驾驶舱字段（后端契约）
      if (shot === undefined && m.shot !== undefined) shot = m.shot
      if (url === undefined && m.url !== undefined) url = m.url
      if (cons === undefined && m.console != null) cons = m.console
      if (!snapshot) snapshot = browserSnapshotText(m)
      if (shot !== undefined && url !== undefined && cons !== undefined && snapshot) break
    }
    return { shot: shot ?? '', url: url ?? '', console: cons ?? [], snapshot, running }
  })

  // 代码块「应用到文件」：直接写入（改了就是改了，无确认步骤），成功后本地补一张
  // 写入卡（路径 + diff）——写入不进账本（非模型轮次），卡片是本进程内的即时回执
  //（与 restoreWrite 的"已恢复"卡同构）。
  async function proposeApplyCode(path: string, code: string) {
    error.value = ''
    try {
      const res = await bridge().app.ProposeFileWrite(sessionId.value, path, code)
      if (res) {
        const c = ensureConvo(sessionId.value)
        const card = withId({
          role: 'tool' as const,
          content: `written ${res.path} (${res.bytes} bytes)`,
          toolName: 'fs',
          status: 'success',
          title: res.path,
          op: res.isNew ? 'write' : 'edit',
          diff: res.diff,
          at: Date.now(),
        })
        const ast = inFlightAssistant(c)
        const i = ast ? c.messages.indexOf(ast) + 1 : c.messages.length
        c.messages.splice(i, 0, card)
      }
      return res
    } catch (e) {
      error.value = errText(e)
      throw e
    }
  }

  // ---- 用户自己的命令行（0.3 最小能力）----
  // 执行复用现有 shell 工具与审批闸门（后端 RunUserCommand：同一超时、同一审批卡），
  // 结果落一张本地工具卡（与"点链接开浏览器"同款：这不是对话回合，不落账本）。
  async function runUserCommand(command: string) {
    const id = sessionId.value
    error.value = ''
    const cmd = command.trim()
    if (!cmd) return
    const c = ensureConvo(id)
    const card = withId({
      role: 'tool' as const,
      content: '执行中…',
      toolName: 'shell',
      status: 'running',
      title: cmd,
      op: 'exec',
      at: Date.now(),
    })
    c.messages.push(card)
    try {
      const res = await bridge().app.RunUserCommand(id, cmd)
      card.status = res?.isError ? 'error' : 'success'
      card.content = res?.output || '(无输出)'
    } catch (e) {
      card.status = 'error'
      card.content = errText(e)
    }
  }

  // ---- 提交说明（0.3 最小能力）----
  // suggestCommitMessage：后端取工作区 diff，用当前模型生成一条提交说明（不落账本）。
  async function suggestCommitMessage(): Promise<string> {
    error.value = ''
    try {
      return (await bridge().app.SuggestCommitMessage(sessionId.value)) ?? ''
    } catch (e) {
      error.value = errText(e)
      throw e
    }
  }

  // gitStageAndCommit：git add -A + commit。前端必须先弹确认框（diff → 说明 → 人确认），
  // 这里只负责把已确认的说明交给后端；push/reset/clean 之类的改写路径后端根本不存在。
  async function gitStageAndCommit(message: string): Promise<string> {
    error.value = ''
    try {
      return (await bridge().app.GitStageAndCommit(sessionId.value, message)) ?? ''
    } catch (e) {
      error.value = errText(e)
      throw e
    }
  }

  function onTerminal(p: { sessionID: string; endReason: number; error: string }) {
    const c = ensureConvo(p.sessionID)
    // 中断语义必须在复位**前**快照：下面两行会把 stopping 清回 false，
    // 之后再判 !c.stopping 恒真——守卫形同虚设（此前"点了中断不续发队列"
    // 全靠 stop() 顺手清队列兜着）。endReason=CANCELLED 同属用户中断。
    const interrupted = c.stopping || p.endReason === END_REASON.CANCELLED
    c.running = false
    c.stopping = false
    clearStopTimer(p.sessionID) // 终态到达：撤销中断超时兜底
    const ast = inFlightAssistant(c)
    if (ast) {
      ast.streaming = false
      ast.term = p.endReason
      if (p.endReason !== END_REASON.DONE) {
        ast.error = true
        ast.content += (ast.content ? '\n\n' : '') + terminalLabel(p.endReason, p.error)
      } else if (!ast.content && !ast.thinking) {
        // 正常结束但占位段仍是"正在思考"空壳（模型只调了工具等）：
        // 默认移除空占位——零块 DONE 不补空气泡的既有契约（此处连占位也不留）。
        // 例外（0.0.25）：本轮带了附件时**不删**，改写一句"模型没有返回内容"——
        // 附件轮的空回答必须看得见，否则用户分不清"模型没答"和"附件没发出去"。
        const i = c.messages.indexOf(ast)
        if (i >= 0 && turnHadAttachments(c, i)) {
          ast.content = '模型没有返回内容'
        } else if (i >= 0) {
          c.messages.splice(i, 1)
        }
      }
      // 本轮耗时（Cline 惯例）：send 置位、terminal 收算；无进行中轮次（测试直插）不计
      if (c.turnStartedAt > 0) {
        ast.durationMs = Date.now() - c.turnStartedAt
        c.turnStartedAt = 0
      }
    } else if (p.endReason !== END_REASON.DONE) {
      // 零块终态：整个回合没有任何增量到达就结束了（无可用渠道 / 流未建立即失败）。
      // 此时没有气泡可挂错误——必须补一条，否则界面表现为"消息发出去了，什么都没发生"，
      // 用户完全不知道出了什么事（实机反馈："没法进行对话"）。
      c.messages.push(
        withId({
          role: 'assistant',
          content: terminalLabel(p.endReason, p.error),
          error: true,
          term: p.endReason,
          at: Date.now(),
        }),
      )
      c.turnStartedAt = 0
    }
    // 首轮结束自动命名会话（开源惯例：open-webui/lobe-chat 以首条消息截断作标题，
    // 侧栏不再裸奔会话 ID）；用户重命名过的不覆盖——titleOf 回退会话 ID 即"未命名"判据。
    // 失败会落进 error 位（可见），不静默。
    const firstUser = c.messages.find((m) => m.role === 'user')?.content ?? ''
    const title = firstUser.replace(/\s+/g, ' ').trim().slice(0, 20)
    if (title && titleOf(p.sessionID) === p.sessionID) {
      void renameSession(p.sessionID, title) // 内部成功后刷新摘要列表
    } else {
      void loadSessions() // 新会话首聊后进入列表
    }
    // 队列：本轮结束自动发出下一条（发给"刚结束的这个会话"，即使它已不是当前视图）。
    // 用户点了中断：这一轮结束，不要自动把队列里的下一条发出去（重新入队的留给用户手动发）
    if (!interrupted) {
      const next = c.queue.shift()
      // 附件与强制工具随队列续发（0.0.11 / 第 7 批）：sendTo 内部按需分派
      if (next) sendTo(p.sessionID, next.text, next.atts, { forced: next.forced }).catch(() => {}) // 队列续发失败：错误气泡已可见
    }
  }

  // 删除会话：删除后若删的是当前会话，则新建空会话
  // （否则界面会停在一个已不存在的会话上，后续 Replay 只会得到空内容）。
  async function removeSession(id: string) {
    if (isRunning(id)) {
      // 运行中删除会让正在写账本的轮次踩空（账本句柄被关闭/文件被删）。
      // 必须显式拒绝且可见——静默 return 会让用户以为"点了没反应"。
      error.value = '该会话正在运行，请先中断再删除'
      return
    }
    error.value = ''
    try {
      await bridge().app.DeleteSession(id)
    } catch (e) {
      error.value = errText(e)
      return
    }
    convos.delete(id) // 缓冲一并丢弃：会话已不存在，留着只会"复活"出幽灵消息
    await loadSessions()
    if (sessionId.value === id) await newSession()
  }

  // 导出会话 Markdown（返回文本，复制/保存由调用方决定）
  async function exportMarkdown(id: string): Promise<string> {
    error.value = ''
    try {
      return (await bridge().app.ExportSessionMarkdown(id)) ?? ''
    } catch (e) {
      error.value = errText(e)
      return ''
    }
  }

  // 中断超时兜底：终态事件若丢失，该会话会永久"运行中"（中断按钮 disabled、
  // 删除也被拒——用户只能重启应用）。10 秒未收到终态即强制复位并明示。
  const stopTimers = new Map<string, number>()
  function clearStopTimer(id: string) {
    const t = stopTimers.get(id)
    if (t !== undefined) {
      window.clearTimeout(t)
      stopTimers.delete(id)
    }
  }

  // 中断"当前正在看的会话"（按钮就长在它的输入框上）。
  function stop() {
    const id = sessionId.value
    if (!id) return
    const c = convoOf(id)
    if (!c || !c.running || c.stopping) return
    c.stopping = true
    c.queue = [] // 中断是停掉这一轮，不能在终态后把排队消息接着发出去
    void bridge().app.Stop(id)
    clearStopTimer(id)
    stopTimers.set(
      id,
      window.setTimeout(() => {
        stopTimers.delete(id)
        const cur = convos.get(id)
        if (!cur?.running) return
        cur.running = false
        cur.stopping = false
        const ast = inFlightAssistant(cur)
        if (ast) {
          ast.streaming = false
          ast.error = true
          ast.content += (ast.content ? '\n\n' : '') + '⚠ 未收到中断确认，已强制复位'
        }
        error.value = '未收到中断确认：已强制复位该会话的运行状态'
      }, 10_000),
    )
  }

  // 输入队列（0.2.14）：回合进行中的提交依次排队，终态后自动逐条发出（绝不与进行中轮次并发）。
  // 多会话（0.2.25）：队列按会话各一份，后台会话的队列续发不进当前视图。
  let queueSeq = 0
  // 入队（0.0.11：附件随行）：附件拷贝一份——调用方随后会清空待发送区，
  // 队列里这条必须自持（否则续发时附件已被清掉，消息静默丢附件）。
  function enqueue(text: string, atts: PendingAttachment[] = [], forced?: ForcedToolDTO) {
    ensureConvo(sessionId.value).queue.push({ id: ++queueSeq, text, atts: [...atts], forced })
  }
  function removeQueued(id: number) {
    const c = ensureConvo(sessionId.value)
    c.queue = c.queue.filter((q) => q.id !== id)
  }
  function promoteQueued(id: number) {
    const c = ensureConvo(sessionId.value)
    const i = c.queue.findIndex((q) => q.id === id)
    if (i > 0) c.queue.unshift(...c.queue.splice(i, 1))
  }
  // 取回编辑：返回文字与附件并出队（调用方负责放回输入框/待发送区）
  function editQueued(id: number): { text: string; atts: PendingAttachment[] } | undefined {
    const c = ensureConvo(sessionId.value)
    const q = c.queue.find((x) => x.id === id)
    if (!q) return undefined
    removeQueued(id)
    return { text: q.text, atts: q.atts }
  }

  async function init() {
    await loadSessions()
    // 抢跑保护（0.2.31 实机："打开软件就直接进行对话没显示"）：init 的 IPC 往返
    // 期间用户可能已经在输入并发送（send 里已领会话 ID、建好视图）——此时
    // 绝不把视图抢到默认会话：用户正在进行的对话必须留在眼前。
    // 没有抢跑时行为不变（选中最近会话 / 无会话则草稿）。
    if (sessionId.value) return
    if (sessions.value.length) await selectSession(sessions.value[0])
    else await newSession()
  }

  return {
    sessionId,
    sessions,
    messages,
    running,
    stopping,
    anyRunning,
    anyPending,
    runningSessions,
    busyTarget,
    pendingOf,
    error,
    summaries,
    loadingSession,
    isRunning,
    titleOf,
    renameSession,
    pinSession,
    loadSessions,
    selectSession,
    loadOlder,
    olderAvailable,
    loadingOlder,
    newSession,
    send,
    onChunk,
    onTool,
    restoreWrite,
    revertRound,
    rerunFrom,
    promptTokens,
    onUsage,
    contextInfo,
    onContext,
    fileDetailPath,
    openFileDetail,
    closeFileDetail,
    rightPanelTab,
    browserOpen,
    openBrowserPanel,
    openLinkInBrowser,
    closeBrowserPanel,
    treeOpen,
    openTreePanel,
    closeTreePanel,
    tasksOpen,
    openTasksPanel,
    closeTasksPanel,
    browserVisual,
    proposeApplyCode,
    runUserCommand,
    suggestCommitMessage,
    gitStageAndCommit,
    onTodo,
    onAsk,
    resolveAsk,
    onTerminal,
    queue,
    enqueue,
    removeQueued,
    promoteQueued,
    editQueued,
    removeSession,
    exportMarkdown,
    onApproval,
    resolveApproval,
    setApprovalPolicy,
    loadApprovalPolicy,
    stop,
    init,
  }
})
