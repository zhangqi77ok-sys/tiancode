import { defineStore } from 'pinia'
import { ref } from 'vue'
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

// 本轮开始时间（send 时置位，terminal 时计算耗时展示；0 表示无进行中轮次）
let turnStartedAt = 0

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

// 生成本地时间会话 ID（草稿首聊落地时才领号）。为什么不用 Date.toISOString：其 UTC 时刻与本地时间观感不一致。
function newSessionId(): string {
  const d = new Date()
  const p = (n: number) => String(n).padStart(2, '0')
  return `s-${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`
}

export const useChatStore = defineStore('chat', () => {
  const sessionId = ref('')
  const sessions = ref<string[]>([])
  const messages = ref<ChatMsg[]>([])
  const running = ref(false)
  // 会话管理类操作的错误（删除等）：与流式错误分开，前者是"操作没生效"，后者是"回复失败"
  const error = ref('')

  // 会话摘要（ID + 标题）：标题为空时侧栏回退显示 ID
  const summaries = ref<SessionSummaryDTO[]>([])

  async function loadSessions() {
    summaries.value = (await bridge().app.ListSessionSummaries()) ?? []
    sessions.value = summaries.value.map((s) => s.id)
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

  async function selectSession(id: string) {
    if (running.value) return // 进行中禁止切换，避免流式块串会话
    // 工具根跟这场对话走。否则点开 A 空间的会话，读写仍打在当前工作区 B 上。
    const sm = summaries.value.find((s) => s.id === id)
    const ws = useWorkspaceStore()
    if (sm?.workspace && sm.workspace !== ws.path) await ws.setPath(sm.workspace)
    else if (sm && !sm.workspace && ws.path) await ws.clear()
    sessionId.value = id
    const history = (await bridge().app.Replay(id)) ?? []
    messages.value = history.map((m) => {
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

  // 新会话 = 草稿：不生成 ID、不进侧栏（侧栏只镜像事件账本）。
  // 修复 0.2.9 回归：反复"打开工作区/新建对话"会把从未落盘的伪会话堆进侧栏当前空间组，
  // 重启才消失。首条消息发出时才在 send() 领 ID 落账本，经 loadSessions 进入侧栏。
  async function newSession() {
    if (running.value) return
    sessionId.value = '' // '' 即草稿态
    messages.value = []
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

  async function send(text: string) {
    if (!sessionId.value) {
      sessionId.value = newSessionId() // 草稿首聊：此刻才领 ID，由后端 Send 落账本
      announcePending()
    }
    messages.value.push(withId({ role: 'user', content: text, at: Date.now() }))
    // 不预建助手占位：助手消息按 ReAct 轮次由 onChunk 按需分段创建（0.2.14），
    // 每轮的思考/文本归属各自轮次，不再全部堆进同一个气泡
    running.value = true
    stopping.value = false
    turnStartedAt = Date.now()
    try {
      // Send 在轮次结束（终态事件已发出）后才 resolve；前置错误走 IPC error
      await bridge().app.Send(sessionId.value, text)
    } catch (e) {
      const ast = inFlightAssistant()
      if (ast) {
        ast.streaming = false
        ast.error = true
        ast.content = `⚠ 出错了：${String(e)}`
      } else {
        // 任何块都未到达就失败（如渠道未配置）：错误必须可见，不静默
        messages.value.push(
          withId({ role: 'assistant', content: `⚠ 出错了：${String(e)}`, error: true, at: Date.now() }),
        )
      }
      running.value = false
      stopping.value = false
    }
  }

  // 本轮占位助手：工具卡会插在它前面，不能用 messages.at(-1)。
  function inFlightAssistant(): ChatMsg | undefined {
    return messages.value.find((m) => m.role === 'assistant' && m.streaming)
  }

  // 审批卡片：内核要"问"时插入一张带允许/拒绝按钮的卡片（ADR-0007）
  function onApproval(p: { id: string; toolName: string; arguments: string }) {
    messages.value.push(
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

  // 提交答复：失败必须可见（例如"已处理"），绝不静默
  async function resolveApproval(id: string, approved: boolean, reason = '') {
    error.value = ''
    try {
      await bridge().app.ResolveApproval(id, approved, reason)
    } catch (e) {
      error.value = String(e instanceof Error ? e.message : e)
      return
    }
    const card = messages.value.find((m) => m.approvalId === id)
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

  // 事件桥回调（App.vue onMounted 绑定）。
  function onChunk(p: { sessionID: string; delta: string; thinking: string }) {
    if (p.sessionID !== sessionId.value) return
    let ast = inFlightAssistant()
    if (!ast) {
      // ReAct 新轮次：无进行中助手则新开一段——工具卡之后的增量落进下一段
      ast = withId({ role: 'assistant', content: '', streaming: true, at: Date.now() })
      messages.value.push(ast)
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
    if (p.sessionID !== sessionId.value) return
    if (p.name === 'todo') return // 任务清单由 onTodo/FloatingTodo 承载，不重复出工具卡
    if (p.name === 'ask_user') return // 问答卡由 onAsk/AskCard 承载，答案已在卡上
    // 封存当前段：ReAct 叙事顺序 = 本轮思考/文本 → 工具卡 → 下一段（onChunk 再开新段）
    const ast = inFlightAssistant()
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
    const i = ast ? messages.value.indexOf(ast) + 1 : messages.value.length
    messages.value.splice(i, 0, card)
  }

  // 任务清单：单卡原地更新（同会话只保留一张，位置保留首次出现处）
  function onTodo(p: { sessionID: string; items: TodoItem[] }) {
    if (p.sessionID !== sessionId.value) return
    const existing = messages.value.find((m) => m.role === 'todo')
    if (existing) {
      existing.todos = p.items
      return
    }
    const ast = inFlightAssistant()
    const card = withId({ role: 'todo', content: '', todos: p.items, at: Date.now() })
    const i = ast ? messages.value.indexOf(ast) : messages.value.length
    messages.value.splice(i, 0, card)
  }

  // 问答卡（ask_user）：插入待答卡片并封存当前段——叙事顺序 = 本轮文本 → 问答卡 → 回复
  function onAsk(p: { id: string; question: string; options?: string[] }) {
    const ast = inFlightAssistant()
    if (ast) ast.streaming = false
    messages.value.push(
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
    const card = messages.value.find((m) => m.askId === id)
    if (card) {
      card.answered = true
      card.content = answer
    }
  }

  function onTerminal(p: { sessionID: string; endReason: number; error: string }) {
    if (p.sessionID !== sessionId.value) return
    running.value = false
    stopping.value = false
    const ast = inFlightAssistant()
    if (ast) {
      ast.streaming = false
      ast.term = p.endReason
      if (p.endReason !== END_REASON.DONE) {
        ast.error = true
        ast.content += (ast.content ? '\n\n' : '') + terminalLabel(p.endReason, p.error)
      }
      // 本轮耗时（Cline 惯例）：send 置位、terminal 收算；无进行中轮次（测试直插）不计
      if (turnStartedAt > 0) {
        ast.durationMs = Date.now() - turnStartedAt
        turnStartedAt = 0
      }
    } else if (p.endReason !== END_REASON.DONE) {
      // 零块终态：整个回合没有任何增量到达就结束了（无可用渠道 / 流未建立即失败）。
      // 此时没有气泡可挂错误——必须补一条，否则界面表现为"消息发出去了，什么都没发生"，
      // 用户完全不知道出了什么事（实机反馈："没法进行对话"）。
      messages.value.push(
        withId({
          role: 'assistant',
          content: terminalLabel(p.endReason, p.error),
          error: true,
          term: p.endReason,
          at: Date.now(),
        }),
      )
      turnStartedAt = 0
    }
    // 首轮结束自动命名会话（开源惯例：open-webui/lobe-chat 以首条消息截断作标题，
    // 侧栏不再裸奔会话 ID）；用户重命名过的不覆盖——titleOf 回退会话 ID 即"未命名"判据。
    // 失败会落进 error 位（可见），不静默。
    const firstUser = messages.value.find((m) => m.role === 'user')?.content ?? ''
    const title = firstUser.replace(/\s+/g, ' ').trim().slice(0, 20)
    if (title && titleOf(p.sessionID) === p.sessionID) {
      void renameSession(p.sessionID, title) // 内部成功后刷新摘要列表
    } else {
      void loadSessions() // 新会话首聊后进入列表
    }
    // 队列：本轮结束自动发出下一条
    // 用户点了中断：这一轮结束，不要自动把队列里的下一条发出去
    if (p.endReason !== END_REASON.CANCELLED && !stopping.value) {
      const next = queue.value.shift()
      if (next) void send(next.text)
    }
  }

  // 删除会话：删除后若删的是当前会话，则新建空会话
  // （否则界面会停在一个已不存在的会话上，后续 Replay 只会得到空内容）。
  async function removeSession(id: string) {
    if (running.value) return // 流式中禁止删除，避免读到半截账本
    error.value = ''
    try {
      await bridge().app.DeleteSession(id)
    } catch (e) {
      error.value = String(e instanceof Error ? e.message : e)
      return
    }
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

  const stopping = ref(false)

  function stop() {
    if (!sessionId.value || !running.value || stopping.value) return
    stopping.value = true
    queue.value = [] // 中断是停掉这一轮，不能在终态后把排队消息接着发出去
    void bridge().app.Stop(sessionId.value)
  }

  // 输入队列（0.2.14）：回合进行中的提交依次排队，终态后自动逐条发出（绝不与进行中轮次并发）
  let queueSeq = 0
  const queue = ref<{ id: number; text: string }[]>([])
  function enqueue(text: string) {
    queue.value.push({ id: ++queueSeq, text })
  }
  function removeQueued(id: number) {
    queue.value = queue.value.filter((q) => q.id !== id)
  }
  function promoteQueued(id: number) {
    const i = queue.value.findIndex((q) => q.id === id)
    if (i > 0) queue.value.unshift(...queue.value.splice(i, 1))
  }
  // 取回编辑：返回文本并出队（调用方负责放回输入框）
  function editQueued(id: number): string | undefined {
    const q = queue.value.find((x) => x.id === id)
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
    error,
    summaries,
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
