import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, defineComponent, h } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import FileDetailPanel from './FileDetailPanel.vue'
import { useChatStore } from '../stores/chat'

// 第 3 批：右侧文件详情面板——逐次 diff + 累计统计 + 关闭。

// 第 8 批：只读正文（按这场对话的工作区读；越界/二进制由后端显式报错）
const bodyCalls = vi.hoisted(() => ({
  reads: [] as string[],
  fail: '',
  // 按路径定制返回正文（缺省仍是无差别正文，既有用例不受影响）
  contents: {} as Record<string, string>,
  // 慢请求门：列入 held 的路径，其响应挂起直到 releaseHeld()——复现"换了文件，
  // 旧文件的响应才回来"的过期时序
  held: [] as string[],
  pending: [] as Array<() => void>,
}))

function releaseHeld() {
  const rs = bodyCalls.pending
  bodyCalls.pending = []
  for (const r of rs) r()
}

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ReadSessionFile: async (_sid: string, path: string) => {
        bodyCalls.reads.push(path)
        if (bodyCalls.fail) throw new Error(bodyCalls.fail)
        if (bodyCalls.held.includes(path)) {
          await new Promise<void>((r) => bodyCalls.pending.push(r))
        }
        return {
          path,
          content: bodyCalls.contents[path] ?? 'package app\n\nfunc main() {}',
          truncated: false,
          limit: 10 << 20,
        }
      },
      RevealInExplorer: async () => {},
      RestoreToolWrite: async () => 'ok',
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
  winClose: async () => {},
  winMinimize: async () => {},
  winToggleMaximize: async () => {},
}))

let host: HTMLElement
let app: ReturnType<typeof createApp> | null = null
let pinia: ReturnType<typeof createPinia>

async function mountPanel(): Promise<HTMLElement> {
  host = document.createElement('div')
  document.body.appendChild(host)
  app = createApp(defineComponent({ render: () => h(FileDetailPanel) }))
  app.use(pinia)
  app.mount(host)
  await nextTick()
  return host
}

function teardown() {
  app?.unmount()
  app = null
  host?.remove()
}

async function seedTwoChanges() {
  const store = useChatStore()
  await store.newSession()
  await store.send('hi')
  store.onTool({
    sessionID: store.sessionId,
    name: 'fs',
    status: 'success',
    summary: 'w',
    callID: 'c1',
    title: 'internal/a.go',
    op: 'edit',
    diff: '-x\n+y\n',
  })
  store.onTool({
    sessionID: store.sessionId,
    name: 'fs',
    status: 'success',
    summary: 'w',
    callID: 'c2',
    title: 'internal/a.go',
    op: 'edit',
    diff: '-p\n+q\n+r\n',
  })
  store.onTool({
    sessionID: store.sessionId,
    name: 'fs',
    status: 'success',
    summary: 'w',
    callID: 'c3',
    title: 'other/b.go',
    op: 'edit',
    diff: '-m\n',
  })
  return store
}

describe('FileDetailPanel（第 3 批）', () => {
  beforeEach(() => {
    teardown()
    pinia = createPinia()
    setActivePinia(pinia)
    bodyCalls.reads = []
    bodyCalls.fail = ''
    bodyCalls.contents = {}
    bodyCalls.held = []
    bodyCalls.pending = []
  })

  // 第 8 批：面板在 diff 上方显示只读正文；本会话没有变更时仍有正文
  it('无变更的文本文件也显示只读正文', async () => {
    const store = useChatStore()
    store.openFileDetail('internal/app/main.go')
    const el = await mountPanel()
    await nextTick()
    expect(bodyCalls.reads).toEqual(['internal/app/main.go']) // 按会话工作区读
    expect(el.textContent).toContain('该文件在本会话没有变更记录')
    expect(el.textContent).toContain('package app') // 正文照显示
    expect(el.querySelector('textarea')).toBeNull() // 只读：没有编辑框
  })

  // 第 8 批：读不到时写明原因，不用空白冒充已读
  it('越界/读失败：显示原因、不显示正文块', async () => {
    bodyCalls.fail = '路径不在该对话的工作区内：../escape.go'
    const store = useChatStore()
    store.openFileDetail('../escape.go')
    const el = await mountPanel()
    await nextTick()
    expect(el.textContent).toContain('路径不在该对话的工作区内')
    expect(el.textContent).not.toContain('package app')
  })

  // 只列当前文件：逐次改动 + 累计统计（其他文件不出现）
  it('渲染该文件的逐次改动与累计统计', async () => {
    const store = await seedTwoChanges()
    store.openFileDetail('internal/a.go')
    const el = await mountPanel()
    const text = el.textContent ?? ''
    expect(text).toContain('2 处改动')
    expect(text).toContain('第 1 / 2 处')
    expect(text).toContain('第 2 / 2 处')
    expect(text).toContain('+3')
    expect(text).toContain('-2')
    expect(text).not.toContain('b.go') // 只显示打开的那个文件
  })

  // 关闭按钮 → fileDetailPath 清空（App 层据此卸载面板）
  // 0.3 起正文区有"编辑"等按钮，不再假设"DOM 最后一个按钮 = 关闭"，按 title 精确选中
  it('关闭按钮清空打开状态', async () => {
    const store = await seedTwoChanges()
    store.openFileDetail('internal/a.go')
    const el = await mountPanel()
    const closeBtn = el.querySelector('button[title="关闭"]') as HTMLButtonElement
    expect(closeBtn).toBeTruthy()
    await closeBtn.click()
    expect(store.fileDetailPath).toBe('')
  })

  // 快速换文件：慢的旧响应回来时不得覆盖当前文件正文——面板标题是 B，正文必须
  // 还是 B 的（与 FileTreePanel 的 gen、BrowserPanel 的 fetchSeq 同一守卫纪律）
  it('快速换文件：过期响应丢弃，不覆盖当前正文', async () => {
    const store = useChatStore()
    bodyCalls.contents = { 'a.go': '甲文件正文', 'b.go': '乙文件正文' }
    bodyCalls.held = ['a.go']
    store.openFileDetail('a.go')
    const el = await mountPanel()
    await nextTick()
    expect(bodyCalls.reads).toEqual(['a.go'])
    store.openFileDetail('b.go')
    await nextTick()
    await nextTick()
    expect(bodyCalls.reads).toEqual(['a.go', 'b.go'])
    expect(el.textContent).toContain('乙文件正文') // 新请求先行返回
    releaseHeld()
    // 等一个宏任务：让被放行的旧响应把"恢复 → 写入 → 渲染"整条链跑完再断言，
    // 不依赖微任务（nextTick）与该链条的相对顺序
    await new Promise((r) => setTimeout(r, 0))
    expect(el.textContent).toContain('乙文件正文') // 旧响应回来也不许覆盖
    expect(el.textContent).not.toContain('甲文件正文')
  })
})
