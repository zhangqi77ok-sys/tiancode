import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import FileTreePanel from './FileTreePanel.vue'
import { useChatStore } from '../stores/chat'

// 右栏「目录」tab：懒加载逐层展开的工作区文件树（根 = 这场对话的工作区）。
// 每层只在点开时经 ListWorkspaceDir 拉取；点文件走现有 openFileDetail；
// 加载/空/错误态齐全；会话切换重载（根随会话归属变）。

const calls = vi.hoisted(() => ({
  dirs: [] as { sessionID: string; rel: string }[],
  entries: {} as Record<
    string,
    { name: string; isDir: boolean; modTime: number }[]
  >,
  fail: '',
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ListWorkspaceDir: async (sessionID: string, rel: string) => {
        calls.dirs.push({ sessionID, rel })
        if (calls.fail) throw new Error(calls.fail)
        return calls.entries[rel] ?? []
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
  app = createApp(defineComponent({ render: () => h(FileTreePanel) }))
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
  await store.send('看看项目结构')
  return store
}

describe('FileTreePanel（右栏「目录」tab）', () => {
  beforeEach(() => {
    teardown()
    pinia = createPinia()
    setActivePinia(pinia)
    calls.dirs = []
    calls.entries = {}
    calls.fail = ''
  })

  it('挂载即拉根目录（rel=""，带当前会话 ID），目录在前逐行渲染', async () => {
    calls.entries[''] = [
      { name: 'z-dir', isDir: true, modTime: 1700000000000 },
      { name: 'a.txt', isDir: false, modTime: 1700000000000 },
    ]
    const store = await seedSession()
    const el = await mountPanel()
    expect(calls.dirs).toEqual([{ sessionID: store.sessionId, rel: '' }])
    const rows = Array.from(el.querySelectorAll<HTMLButtonElement>('[role="treeitem"] > button'))
    expect(rows.map((r) => r.textContent?.trim())).toEqual(['z-dir', 'a.txt'])
    expect(el.textContent).not.toContain('读取中…')
  })

  it('点目录懒加载子层；空目录显示空态', async () => {
    calls.entries[''] = [{ name: 'empty-dir', isDir: true, modTime: 1 }]
    await seedSession()
    const el = await mountPanel()
    expect(calls.dirs.map((c) => c.rel)).toEqual(['']) // 尚未下钻
    const dirRow = Array.from(el.querySelectorAll<HTMLButtonElement>('[role="treeitem"] > button')).find(
      (b) => b.textContent?.includes('empty-dir'),
    )
    await dirRow!.click()
    await nextTick()
    await nextTick()
    expect(calls.dirs.map((c) => c.rel)).toEqual(['', 'empty-dir']) // 点开才拉子层
    expect(el.textContent).toContain('空目录')
  })

  it('点文件走现有 openFileDetail（路径 = 树内相对路径）', async () => {
    calls.entries[''] = [{ name: 'a.txt', isDir: false, modTime: 1 }]
    const store = await seedSession()
    const el = await mountPanel()
    const fileRow = Array.from(el.querySelectorAll<HTMLButtonElement>('[role="treeitem"] > button')).find(
      (b) => b.textContent?.includes('a.txt'),
    )
    await fileRow!.click()
    expect(store.fileDetailPath).toBe('a.txt')
    expect(store.rightPanelTab).toBe('file')
  })

  it('根读取失败（如无工作区）：错误可见 + 重试，不用空白冒充空目录', async () => {
    calls.fail = '当前没有工作区——目录树只在选择工作区后可用'
    const store = await seedSession()
    const el = await mountPanel()
    expect(el.textContent).toContain('当前没有工作区')
    expect(el.querySelector('[role="treeitem"]')).toBeNull()
    calls.fail = ''
    calls.entries[''] = [{ name: 'a.txt', isDir: false, modTime: 1 }]
    const retry = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.includes('重试'))
    await retry!.click()
    await nextTick()
    await nextTick()
    expect(el.querySelector('[role="treeitem"]')).toBeTruthy() // 重试后树出来
    expect(store.error === '' || store.error === undefined).toBe(true)
  })

  it('会话切换重载根目录（根随会话归属变化）', async () => {
    calls.entries[''] = [{ name: 'a.txt', isDir: false, modTime: 1 }]
    const store = await seedSession()
    await mountPanel()
    expect(calls.dirs.length).toBe(1)
    store.sessionId = 's-next'
    await nextTick()
    await nextTick()
    expect(calls.dirs.length).toBe(2)
    expect(calls.dirs[1].sessionID).toBe('s-next')
  })
})
