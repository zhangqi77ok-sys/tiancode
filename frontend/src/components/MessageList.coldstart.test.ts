import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createApp, nextTick, watchEffect, type App } from 'vue'

// 真实 DOM 冷启动集成测试（0.2.31）：复刻用户实机场景——
// "打开软件直接对话，消息没显示；切换一下会话就出来了"。
// 这里用真实 MessageList + 真实 store 挂到 DOM 上，断言"发送后消息必须可见"。
const h = vi.hoisted(() => ({
  summaries: [] as { id: string; title: string; lastActiveMs?: number }[],
  replayById: {} as Record<
    string,
    { role: string; content: string; thinking?: string; toolName?: string; status?: string }[]
  >,
  sends: [] as { sessionID: string; text: string }[],
  // 门闸：模拟慢 IPC（ListSessionSummaries / Replay）
  summariesGate: null as Promise<void> | null,
  replayGate: null as Promise<void> | null,
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ListSessions: async () => h.summaries.map((s) => s.id),
      ListSessionSummaries: async () => {
        if (h.summariesGate) await h.summariesGate
        return h.summaries
      },
      Replay: async (id: string) => {
        if (h.replayGate) await h.replayGate
        return h.replayById[id] ?? []
      },
      Send: async (sessionID: string, text: string) => {
        h.sends.push({ sessionID, text })
      },
      Stop: async () => {},
      RenameSession: async () => {},
      ListChannels: async () => ({ channels: [], activeId: '' }),
      ApprovalPolicy: async () => [],
      GetWorkspace: async () => '',
    },
    runtime: { EventsOn: () => {} },
  }),
}))

const { useChatStore } = await import('../stores/chat')
const MessageList = (await import('./MessageList.vue')).default

let host: HTMLElement
let app: App | null = null

function mountList(): HTMLElement {
  host = document.createElement('div')
  document.body.appendChild(host)
  // 组件与测试必须共用同一个 pinia：两个实例会让测试对 A 操作、组件渲染 B（恒空）
  const pinia = createPinia()
  setActivePinia(pinia)
  app = createApp(MessageList)
  app.use(pinia)
  app.mount(host)
  return host
}

// 历史会话（"你好"）：末轮 = user 提问 + assistant 长回答（含代码块）
const history = [
  { role: 'user', content: '你好' },
  { role: 'assistant', content: '你好！有什么可以帮你？' },
  { role: 'user', content: '写一个快排并解释思路' },
  { role: 'assistant', content: '快排是分治：选一个基准值……代码略' },
]

async function flush(times = 4) {
  for (let i = 0; i < times; i++) await nextTick()
}

describe('冷启动直接对话（DOM 集成）', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    h.summaries = []
    h.replayById = {}
    h.sends = []
    h.summariesGate = null
    h.replayGate = null
    document.body.innerHTML = ''
    app?.unmount()
    app = null
  })

  // 主场景：打开软件 → init 选中历史会话 → 直接在输入框发消息 → 必须立刻看到
  it('init 完成后发送：消息立即可见', async () => {
    h.summaries = [{ id: 's-HI', title: '你好', lastActiveMs: 1 }]
    h.replayById['s-HI'] = history
    const el = mountList()
    const store = useChatStore() // 必须 mount 之后取：与组件共用同一 pinia

    await store.init()
    await flush()
    // 历史必须真实渲染（用仅存在于历史的 assistant 文本断言——建议 chips 里
    // 也有"写一个快排并解释思路"字样，用它会假通过）
    expect(store.messages.length).toBe(4)
    expect(el.textContent).toContain('你好！有什么可以帮你？')
    expect(el.textContent).not.toContain('未选择工作区 · 纯对话')

    await store.send('写一个快排并解释思路')
    await flush()
    // 发送后：新消息可见（历史 4 条 + 新 user + 思考占位）
    expect(store.messages.length).toBe(6)
    const hits = el.textContent?.split('写一个快排并解释思路').length ?? 0
    expect(hits).toBeGreaterThan(1)
    expect(el.textContent).not.toContain('未选择工作区 · 纯对话')
  })

  // 阶段 2：上百条消息的长会话只挂窗口内的回合——但"最新一条"必须始终在 DOM 里
  // （贴底跟随、发送后到底、切换会话落最新都指着尾部这一条）。
  it('长会话只挂尾部窗口，最新一条始终可见', async () => {
    const many: { role: string; content: string }[] = []
    for (let i = 0; i < 200; i++) {
      many.push({ role: i % 2 === 0 ? 'user' : 'assistant', content: `第 ${i} 条` })
    }
    h.summaries = [{ id: 's-long', title: '长会话', lastActiveMs: 1 }]
    h.replayById['s-long'] = many

    const el = mountList()
    const store = useChatStore()
    await store.init()
    await flush()

    expect(store.messages.length).toBe(200)
    // 滚动容器的直接子元素就是当前挂着的回合（顺序与键与完整分组一致）
    const scroller = el.querySelector('[data-conversation]') as HTMLElement
    expect(scroller.children.length).toBe(40) // WINDOW_INITIAL_TAIL：不是 200 个回合全挂
    expect(el.textContent).toContain('第 199 条') // 尾部挂着
    expect(el.textContent).not.toContain('第 0 条') // 最老的一段没挂
  })

  // 抢跑场景：用户快于 init（loadSessions 还挂着）就发送。核心契约：
  //   1) 消息进用户自己领取的会话（数据立即正确）；
  //   2) init 完成后**不抢视图**（用户正在进行的对话留在眼前，0.2.31 抢跑保护）；
  //   3) 消息不丢且 DOM 可见。
  it('抢跑（init 未完成就发送）：不抢视图、消息不丢且可见', async () => {
    h.summaries = [{ id: 's-HI', title: '你好', lastActiveMs: 1 }]
    h.replayById['s-HI'] = history
    let releaseSummaries: () => void = () => {}
    h.summariesGate = new Promise<void>((r) => {
      releaseSummaries = r
    })

    const el = mountList()
    const store = useChatStore() // 必须 mount 之后取：与组件共用同一 pinia

    const initP = store.init() // 挂起在 loadSessions
    await store.send('抢跑的消息')
    await flush()
    const draftId = store.sessionId
    expect(draftId).not.toBe('')
    expect(store.messages.length).toBe(2) // user + 思考占位（数据立即正确）

    releaseSummaries()
    await initP
    await flush()
    // 抢跑保护：init 完成不把视图抢到默认会话
    expect(store.sessionId).toBe(draftId)
    expect(store.messages.length).toBe(2)
    expect(el.textContent).toContain('抢跑的消息')
  })

  // Replay 挂起（慢 IPC）时发送：载入完成后合并，消息不丢、DOM 可见
  it('Replay 挂起时发送：载入完成后消息仍在且可见', async () => {
    h.summaries = [{ id: 's-HI', title: '你好', lastActiveMs: 1 }]
    h.replayById['s-HI'] = history
    let releaseReplay: () => void = () => {}
    h.replayGate = new Promise<void>((r) => {
      releaseReplay = r
    })

    const el = mountList()
    const store = useChatStore() // 必须 mount 之后取：与组件共用同一 pinia

    const initP = store.init() // 进入 selectSession → Replay 挂起
    await flush(2)
    await store.send('窗口期的消息')
    await flush()
    // 载入提示期间：消息已在缓冲（可见性由“载入完成后”保证）
    releaseReplay()
    await initP
    await flush()
    expect(el.textContent).toContain('窗口期的消息')
    expect(el.textContent).toContain('写一个快排并解释思路')
  })
})
