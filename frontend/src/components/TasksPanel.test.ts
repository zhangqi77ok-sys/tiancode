import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import TasksPanel from './TasksPanel.vue'
import { useChatStore } from '../stores/chat'

// 右栏「任务」tab：本会话 shell bg_start 后台任务快照（id/命令/状态/日志）。
// 数据 = 后端 BgTasksSnapshot（shell 工具内存任务表）：面板打开期间 2s 轮询，
// 卸载即停；会话切换立即重取。

const calls = vi.hoisted(() => ({
  snaps: [] as string[],
  tasks: [] as {
    id: string
    command: string
    pid: number
    running: boolean
    exitCode: number
    startedAt: number
    log: string
  }[],
  fail: '',
  // 可控延迟：指定会话的快照挂起（模拟旧会话慢响应，测过期结果丢弃）
  gate: null as Promise<void> | null,
  gateSid: '',
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      BgTasksSnapshot: async (sessionID: string) => {
        calls.snaps.push(sessionID)
        if (calls.gate && sessionID === calls.gateSid) await calls.gate
        if (calls.fail) throw new Error(calls.fail)
        return calls.tasks
      },
      ListSessionSummaries: async () => [],
      Replay: async () => [],
      Send: async () => {},
      Stop: async () => {},
      RenameSession: async () => {},
      ResolveApproval: async () => {},
      ResolveAsk: async () => {},
      ApprovalPolicy: async () => [],
      SetApprovalPolicy: async () => {},
      DeleteSession: async () => {},
    },
    runtime: { EventsOn: () => {} },
  }),
}))

let host: HTMLElement
let app: ReturnType<typeof createApp> | null = null
let pinia: ReturnType<typeof createPinia>

async function mountPanel() {
  host = document.createElement('div')
  document.body.appendChild(host)
  app = createApp(defineComponent({ render: () => h(TasksPanel) }))
  app.use(pinia)
  app.mount(host)
  await nextTick()
  await nextTick()
  return host
}

function teardown() {
  app?.unmount()
  app = null
  host?.remove()
}

async function seedSession() {
  const store = useChatStore()
  await store.newSession()
  await store.send('起个服务看看')
  return store
}

describe('TasksPanel（右栏「任务」tab）', () => {
  beforeEach(() => {
    teardown()
    pinia = createPinia()
    setActivePinia(pinia)
    calls.snaps = []
    calls.tasks = []
    calls.fail = ''
    calls.gate = null
    calls.gateSid = ''
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('渲染任务卡：id / 命令 / 状态（运行中）/ PID / 日志', async () => {
    const store = await seedSession()
    calls.tasks = [
      {
        id: 'bg-1',
        command: 'npm run dev',
        pid: 4242,
        running: true,
        exitCode: -1,
        startedAt: 1700000000000,
        log: 'VITE ready in 300ms',
      },
    ]
    const el = await mountPanel()
    await vi.advanceTimersByTimeAsync(0)
    await nextTick()
    expect(calls.snaps).toEqual([store.sessionId])
    expect(el.textContent).toContain('bg-1')
    expect(el.textContent).toContain('npm run dev')
    expect(el.textContent).toContain('运行中')
    expect(el.textContent).toContain('PID 4242')
    expect(el.textContent).toContain('VITE ready in 300ms')
  })

  it('结束态按退出码区分：0 = 正常结束，非 0 = 退出码 N', async () => {
    await seedSession()
    calls.tasks = [
      { id: 'bg-1', command: 'ok', pid: 1, running: false, exitCode: 0, startedAt: 1, log: '' },
      { id: 'bg-2', command: 'bad', pid: 2, running: false, exitCode: 3, startedAt: 1, log: '' },
    ]
    const el = await mountPanel()
    await vi.advanceTimersByTimeAsync(0)
    await nextTick()
    expect(el.textContent).toContain('正常结束')
    expect(el.textContent).toContain('退出码 3')
  })

  it('无任务显示空态；空态也保持轮询（任务启动后面板能追上）', async () => {
    await seedSession()
    const el = await mountPanel()
    await vi.advanceTimersByTimeAsync(0)
    await nextTick()
    expect(el.textContent).toContain('还没有后台任务')
    expect(calls.snaps).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(2000)
    await nextTick()
    expect(calls.snaps).toHaveLength(2) // 2s 轮询继续
    calls.tasks = [{ id: 'bg-1', command: 'npm run dev', pid: 1, running: true, exitCode: -1, startedAt: 1, log: '' }]
    await vi.advanceTimersByTimeAsync(2000)
    await nextTick()
    expect(el.textContent).toContain('bg-1') // 第三跳追上刚启动的任务
  })

  it('读取失败：错误可见 + 重试；卸载后轮询停止', async () => {
    await seedSession()
    calls.fail = '读取后台任务失败'
    const el = await mountPanel()
    await vi.advanceTimersByTimeAsync(0)
    await nextTick()
    expect(el.textContent).toContain('读取后台任务失败')
    calls.fail = ''
    const retry = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.includes('重试'))
    await retry!.click()
    await nextTick()
    await nextTick()
    expect(el.textContent).not.toContain('读取后台任务失败')
    teardown()
    const n = calls.snaps.length
    await vi.advanceTimersByTimeAsync(6000)
    expect(calls.snaps).toHaveLength(n) // 卸载后不再轮询
  })

  it('会话切换立即重取新会话的快照', async () => {
    const store = await seedSession()
    await mountPanel()
    await vi.advanceTimersByTimeAsync(0)
    expect(calls.snaps).toEqual([store.sessionId])
    store.sessionId = 's-next'
    await vi.advanceTimersByTimeAsync(0)
    await nextTick()
    expect(calls.snaps[calls.snaps.length - 1]).toBe('s-next')
  })

  // 会话切换后代际守卫：旧会话挂起的慢响应返回时必须整体丢弃——
  // 否则旧会话的任务快照会覆盖当前面板（看起来像任务串台）
  it('旧会话的慢响应不覆盖新会话面板', async () => {
    let release!: () => void
    const store = await seedSession()
    calls.gate = new Promise<void>((r) => (release = r))
    calls.gateSid = store.sessionId
    await mountPanel()
    await vi.advanceTimersByTimeAsync(0)
    // 切到新会话：立刻取到新快照（bg-next）
    calls.tasks = [{ id: 'bg-next', command: 'next 里的任务', pid: 2, running: true, exitCode: -1, startedAt: 1, log: '' }]
    store.sessionId = 's-next'
    await vi.advanceTimersByTimeAsync(0)
    await nextTick()
    expect(calls.snaps[calls.snaps.length - 1]).toBe('s-next')
    // 旧会话的慢响应此刻才返回（bg-old）：必须被丢弃
    calls.tasks = [{ id: 'bg-old', command: '旧会话的任务', pid: 1, running: false, exitCode: 0, startedAt: 1, log: '' }]
    release()
    await vi.advanceTimersByTimeAsync(0)
    await nextTick()
    expect(host.textContent ?? '').toContain('bg-next')
    expect(host.textContent ?? '').not.toContain('bg-old')
  })
})
