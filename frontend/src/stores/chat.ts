import { defineStore } from 'pinia'
import { ref } from 'vue'
import { bridge } from '../wails'

// 会话消息的 UI 形态；streaming 标记流式中的临时消息，error 标记错误/取消态；
// role='tool' 为工具卡片（toolName/status 承载卡片数据）；
// at 为消息时间戳（渲染 HH:MM）；term 记录终态枚举（UI 区分 取消/超时 与 错误）。
export interface ChatMsg {
  role: 'user' | 'assistant' | 'tool'
  content: string
  toolName?: string
  status?: string
  at?: number
  term?: number
  streaming?: boolean
  error?: boolean
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

// 生成本地时间会话 ID。为什么不用 Date.toISOString：其 UTC 时刻与本地时间观感不一致。
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

  async function loadSessions() {
    sessions.value = (await bridge().app.ListSessions()) ?? []
  }

  async function selectSession(id: string) {
    if (running.value) return // 进行中禁止切换，避免流式块串会话
    sessionId.value = id
    const history = (await bridge().app.Replay(id)) ?? []
    messages.value = history.map((m) => ({ role: m.role as ChatMsg['role'], content: m.content }))
  }

  async function newSession() {
    if (running.value) return
    sessionId.value = newSessionId()
    messages.value = []
    if (!sessions.value.includes(sessionId.value)) sessions.value.unshift(sessionId.value)
  }

  async function send(text: string) {
    if (!sessionId.value) await newSession()
    messages.value.push({ role: 'user', content: text, at: Date.now() })
    messages.value.push({ role: 'assistant', content: '', streaming: true, at: Date.now() })
    running.value = true
    try {
      // Send 在轮次结束（终态事件已发出）后才 resolve；前置错误走 IPC error
      await bridge().app.Send(sessionId.value, text)
    } catch (e) {
      const last = messages.value[messages.value.length - 1]
      if (last?.streaming) {
        last.streaming = false
        last.error = true
        last.content = `⚠ 出错了：${String(e)}`
      }
      running.value = false
    }
  }

  // 事件桥回调（App.vue onMounted 绑定）。
  function onChunk(p: { sessionID: string; delta: string; thinking: string }) {
    if (p.sessionID !== sessionId.value) return
    const last = messages.value[messages.value.length - 1]
    if (last?.streaming) last.content += p.delta
  }

  function onTool(p: { sessionID: string; name: string; status: string; summary: string }) {
    if (p.sessionID !== sessionId.value) return
    messages.value.push({
      role: 'tool',
      content: p.summary,
      toolName: p.name,
      status: p.status,
      at: Date.now(),
    })
  }

  function onTerminal(p: { sessionID: string; endReason: number; error: string }) {
    if (p.sessionID !== sessionId.value) return
    running.value = false
    const last = messages.value[messages.value.length - 1]
    if (last?.streaming) {
      last.streaming = false
      last.term = p.endReason
      if (p.endReason !== END_REASON.DONE) {
        last.error = true
        last.content += (last.content ? '\n\n' : '') + terminalLabel(p.endReason, p.error)
      }
    }
    void loadSessions() // 新会话首聊后进入列表
  }

  function stop() {
    if (sessionId.value && running.value) bridge().app.Stop(sessionId.value)
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
    loadSessions,
    selectSession,
    newSession,
    send,
    onChunk,
    onTool,
    onTerminal,
    stop,
    init,
  }
})
