import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, defineComponent, h } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import FileDetailPanel from './FileDetailPanel.vue'
import { useChatStore } from '../stores/chat'

// 第 3 批：右侧文件详情面板——逐次 diff + 累计统计 + 关闭。

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
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
  it('关闭按钮清空打开状态', async () => {
    const store = await seedTwoChanges()
    store.openFileDetail('internal/a.go')
    const el = await mountPanel()
    const btns = el.querySelectorAll('button')
    await (btns[btns.length - 1] as HTMLButtonElement).click() // 头部最后一个 = 关闭
    expect(store.fileDetailPath).toBe('')
  })
})
