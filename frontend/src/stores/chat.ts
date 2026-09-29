import { defineStore } from 'pinia'
import { computed, reactive, ref } from 'vue'
import { bridge, type SessionSummaryDTO } from '../wails'
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
  // 审批卡片数据（role='approval'）：id 用于回传答复；args 为原始 JSON 原样展示
  approvalId?: string
  args?: string
  at?: number
  term?: number
  streaming?: boolean
  error?: boolean
  // 本轮耗时（毫秒，terminal 时计算）：AI 工具标配的耗时反馈
  durationMs?: number
  // 会话内唯一 id：列表 key 与折叠态的稳定锚点（下标会在工具卡插入时整体错位）
  id?: string
}

// 消息序号：入库时统一发 id——Replay/事件/本地推送都走这一处
let msgSeq = 0
function withId<T extends Omit<ChatMsg, 'id'>>(m: T): ChatMsg {
  return { ...m, id: `m-${++msgSeq}` }
}

// EndReason 与 core/llm 的枚举一一对应（经事件桥以 int 传输）。
export const END_REASON = { DONE: 1, ERROR: 2, CANCELLED: 3, IDLE_TIMEOUT: 4 } as const

// 终态的人类可读标签：UI 围绕"可区分的三终态"设计（docs/CONTRACTS.md）。
function terminalLabel(reason: number, errText: string): string {
  switch (reason) {
    case END_REASON.CANCELLED:
      return '⚠ 已取消'
    case END_REASON.IDLE_TIMEOUT:
      return '⚠ 响应超时：上游长时间无响应，可重试'
    default:
      return `⚠ 出错了：${errText || '未知错误'}`
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

// 单个会话的运行态（0.2.25 多会话）：消息缓冲 + 运行标志 + 输入队列 + 本轮计时。
// 为什么按会话各一份：回合进行中也允许切到别的会话继续聊——流式事件必须各归各位
// （后台会话的事件照常入它自己的缓冲，不再被丢弃），切回来时原地接着看。
interface Conversation {
  messages: ChatMsg[]
  running: boolean
  stopping: boolean
  queue: { id: number; text: string }[]
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
  function ensureConvo(id: string): Conversation {
    let c = convos.get(id)
    if (!c) {
      c = newConversation()
      convos.set(id, c)
    }
    return c
  }

  // 空缓冲占位：只读路径的返回值（模板读 messages.length 不能拿到 undefined；
  // 常量共享且绝不被写入）
  const EMPTY_MESSAGES: ChatMsg[] = []
  const EMPTY_QUEUE: { id: number; text: string }[] = []

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
    set: (v: { id: number; text: string }[]) => {
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
      error.value = `读取会话列表失败：${String(e instanceof Error ? e.message : e)}`
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
      error.value = String(e instanceof Error ? e.message : e)
    }
  }

  // 从账本恢复一个会话的消息缓冲（仅在该会话本进程内还没有缓冲时调用——
  // 已有缓冲说明本进程内发生过对话甚至正在后台跑，重放只会覆盖掉活数据）。
  async function replayOf(id: string): Promise<ChatMsg[]> {
    const history = (await bridge().app.Replay(id)) ?? []
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
      })
    })
  }

  async function selectSession(id: string) {
    // 工具根跟这场对话走。否则点开 A 空间的会话，读写仍打在当前工作区 B 上。
    // （ws.setPath/clear 内部自带可见报错，失败不阻断切换）
    const sm = summaries.value.find((s) => s.id === id)
    const ws = useWorkspaceStore()
    if (sm?.workspace && sm.workspace !== ws.path) await ws.setPath(sm.workspace)
    else if (sm && !sm.workspace && ws.path) await ws.clear()
    sessionId.value = id
    if (!convos.has(id)) {
      loadingSession.value = true
      try {
        const c = ensureConvo(id)
        c.messages = await replayOf(id)
      } catch (e) {
        // 历史载入失败必须可见（此前是静默空白：用户以为会话内容丢了）
        error.value = `载入会话历史失败：${String(e instanceof Error ? e.message : e)}`
      } finally {
        loadingSession.value = false
      }
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
  function send(text: string) {
    if (!sessionId.value) {
      sessionId.value = newSessionId() // 草稿首聊：此刻才领 ID，由后端 Send 落账本
      announcePending()
    }
    return sendTo(sessionId.value, text)
  }

  // 发送到指定会话（多会话的核心：后台会话的排队续发也走这里，绝不发进当前视图）。
  async function sendTo(id: string, text: string) {
    const c = ensureConvo(id)
    c.messages.push(withId({ role: 'user', content: text, at: Date.now() }))
    // "正在思考"占位（0.2.28）：慢中转/上游挂起时用户立即看到反馈，而不是
    // "消息发出去了，什么都没发生"。onChunk 复用这段（inFlightAssistant 按
    // streaming 段查找）；零块 DONE 终态时移除空占位，不留空气泡。
    c.messages.push(withId({ role: 'assistant', content: '', streaming: true, at: Date.now() }))
    c.running = true
    c.stopping = false
    c.turnStartedAt = Date.now()
    try {
      // Send 在轮次结束（终态事件已发出）后才 resolve；前置错误走 IPC error
      await bridge().app.Send(id, text)
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
    }
  }

  // 本轮占位助手：工具卡会插在它前面，不能用 messages.at(-1)。
  function inFlightAssistant(c: Conversation): ChatMsg | undefined {
    return c.messages.find((m) => m.role === 'assistant' && m.streaming)
  }

  // 审批卡片：内核要"问"时插入一张带允许/拒绝按钮的卡片（ADR-0007）。
  // sessionID 必填：缺标识的卡片宁可丢弃并报错，也绝不插进当前视图（串会话）。
  function onApproval(p: { id: string; sessionID: string; toolName: string; arguments: string }) {
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
      error.value = String(e instanceof Error ? e.message : e)
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
      error.value = String(e instanceof Error ? e.message : e)
    }
  }

  async function setApprovalPolicy(tools: string[]) {
    error.value = ''
    try {
      await bridge().app.SetApprovalPolicy(tools)
    } catch (e) {
      error.value = String(e instanceof Error ? e.message : e)
    }
  }

  async function loadApprovalPolicy(): Promise<string[]> {
    try {
      return (await bridge().app.ApprovalPolicy()) ?? []
    } catch (e) {
      error.value = String(e instanceof Error ? e.message : e)
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

  function onTool(p: {
    sessionID: string
    name: string
    status: string
    summary: string
    content?: string
    diff?: string
    title?: string
    op?: string
  }) {
    const c = ensureConvo(p.sessionID)
    if (p.name === 'todo') return // 任务清单由 onTodo/FloatingTodo 承载，不重复出工具卡
    if (p.name === 'ask_user') return // 问答卡由 onAsk/AskCard 承载，答案已在卡上
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
      at: Date.now(),
    })
    const i = ast ? c.messages.indexOf(ast) + 1 : c.messages.length
    c.messages.splice(i, 0, card)
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
      error.value = String(e instanceof Error ? e.message : e)
      return
    }
    const card = findCard((m) => m.askId === id)
    if (card) {
      card.answered = true
      card.content = answer
    }
  }

  function onTerminal(p: { sessionID: string; endReason: number; error: string }) {
    const c = ensureConvo(p.sessionID)
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
        // 移除空占位——零块 DONE 不补空气泡的既有契约（此处连占位也不留）
        const i = c.messages.indexOf(ast)
        if (i >= 0) c.messages.splice(i, 1)
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
    // 用户点了中断：这一轮结束，不要自动把队列里的下一条发出去
    if (p.endReason !== END_REASON.CANCELLED && !c.stopping) {
      const next = c.queue.shift()
      if (next) void sendTo(p.sessionID, next.text)
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
      error.value = String(e instanceof Error ? e.message : e)
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
      error.value = String(e instanceof Error ? e.message : e)
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
  function enqueue(text: string) {
    ensureConvo(sessionId.value).queue.push({ id: ++queueSeq, text })
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
  // 取回编辑：返回文本并出队（调用方负责放回输入框）
  function editQueued(id: number): string | undefined {
    const c = ensureConvo(sessionId.value)
    const q = c.queue.find((x) => x.id === id)
    if (!q) return undefined
    removeQueued(id)
    return q.text
  }

  async function init() {
    await loadSessions()
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
    newSession,
    send,
    onChunk,
    onTool,
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
