import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import GitPanel from './GitPanel.vue'
import { useChatStore } from '../stores/chat'

// 右栏「Git」tab（0.0.24）：工作区变更 + 单文件 diff + 提交说明。
// 0.0.29 补的契约：数据跟手——模型写文件（treeRev）或切换会话后自动重取，
// 此前只有 onMounted 一次，打开后再发生的改动永远停在旧快照（用户反馈"没有真实效果"）。

const st = vi.hoisted(() => ({
  entries: [] as { path: string; x: string; y: string; untracked: boolean }[],
  statusCalls: [] as string[],
  failStatus: '',
  diffOut: 'diff --git a/a.go b/a.go\n+新行',
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      GitStatusFiles: async (sid: string) => {
        st.statusCalls.push(sid)
        if (st.failStatus) throw new Error(st.failStatus)
        return st.entries
      },
      GitFileDiff: async () => st.diffOut,
      ListSessionSummaries: async () => [],
      Replay: async () => [],
      ReplayTail: async () => ({ messages: [], total: 0, from: 0 }),
      ReplayOlder: async () => ({ messages: [], total: 0, from: 0 }),
      Send: async () => {},
      Stop: async () => {},
    },
    runtime: { EventsOn: () => {} },
  }),
}))

vi.mock('../composables/useDialogs', () => ({
  useDialogs: () => ({ confirm: async () => true, prompt: async () => null }),
}))

const pushed: string[] = []
vi.mock('../composables/useToast', () => ({
  useToast: () => ({ push: (kind: string, text: string) => pushed.push(`${kind}:${text}`) }),
}))

let host: HTMLElement
let app: App | null = null
let pinia: ReturnType<typeof createPinia>

async function mountPanel(): Promise<HTMLElement> {
  host = document.createElement('div')
  document.body.appendChild(host)
  app = createApp(defineComponent({ render: () => h(GitPanel) }))
  app.use(pinia)
  app.mount(host)
  await nextTick()
  await nextTick()
  return host
}

const flush = () => new Promise((r) => setTimeout(r, 0))

beforeEach(() => {
  document.body.innerHTML = ''
  pinia = createPinia()
  setActivePinia(pinia)
  st.entries = []
  st.statusCalls = []
  st.failStatus = ''
  pushed.length = 0
  const store = useChatStore()
  store.sessionId = 's-1'
})

describe('GitPanel（右栏「Git」tab）', () => {
  it('挂载读一次工作区变更：未跟踪/已修改分组显示', async () => {
    st.entries = [
      { path: 'new.txt', x: '', y: '', untracked: true },
      { path: 'a.go', x: 'M', y: '', untracked: false },
    ]
    const el = await mountPanel()
    expect(st.statusCalls).toEqual(['s-1'])
    expect(el.textContent).toContain('new.txt')
    expect(el.textContent).toContain('a.go')
  })

  it('数据跟手（0.0.29）：模型写文件（treeRev）与切换会话都自动重取', async () => {
    st.entries = [{ path: 'a.go', x: 'M', y: '', untracked: false }]
    const el = await mountPanel()
    const afterMount = st.statusCalls.length
    expect(el.textContent).toContain('a.go')

    // 模型写了文件（fs 写/改卡落定 → treeRev +1）→ 自动重取，新文件进面板
    const store = useChatStore()
    st.entries = [
      { path: 'a.go', x: 'M', y: '', untracked: false },
      { path: 'b.go', x: 'M', y: '', untracked: false },
    ]
    store.treeRev++
    await nextTick()
    await flush()
    await nextTick()
    expect(st.statusCalls.length).toBeGreaterThan(afterMount)
    expect(el.textContent).toContain('b.go')

    // 切会话 → 换工作区，重取的是新会话
    const n = st.statusCalls.length
    store.sessionId = 's-2'
    await nextTick()
    await flush()
    expect(st.statusCalls.length).toBeGreaterThan(n)
    expect(st.statusCalls[st.statusCalls.length - 1]).toBe('s-2')
  })

  it('空工作区有明确文案；读取失败错误可见（不用空白冒充"干净"）', async () => {
    const el = await mountPanel()
    expect(el.textContent).toContain('工作区干净')

    document.body.innerHTML = ''
    st.failStatus = '不是 git 仓库'
    const el2 = await mountPanel()
    expect(el2.textContent).toContain('不是 git 仓库')
  })
})
