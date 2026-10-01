import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import BrowserPanel from './BrowserPanel.vue'
import { useChatStore } from '../stores/chat'

// 浏览器驾驶舱（0.0.28）：只读 URL 栏 + 最新截图（ReadBrowserShot 拉图、跟随最新）
// + 控制台尾部 + 元素快照（折叠）。数据源 = store.browserVisual（当前会话缓冲派生）。

const shotCalls = vi.hoisted(() => ({
  calls: [] as string[],
  b64: '',
  fail: '',
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ReadBrowserShot: async (p: string) => {
        shotCalls.calls.push(p)
        if (shotCalls.fail) throw new Error(shotCalls.fail)
        return shotCalls.b64
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
  winClose: async () => {},
  winMinimize: async () => {},
  winToggleMaximize: async () => {},
}))

let host: HTMLElement
let app: ReturnType<typeof createApp> | null = null
let pinia: ReturnType<typeof createPinia>

async function mountPanel() {
  host = document.createElement('div')
  document.body.appendChild(host)
  app = createApp(defineComponent({ render: () => h(BrowserPanel) }))
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

// 开一个会话并按给定序列喂 browser 工具事件
async function seedBrowser(over: Record<string, unknown>) {
  const store = useChatStore()
  await store.newSession()
  await store.send('打开页面看看')
  store.onTool({ sessionID: store.sessionId, name: 'browser', status: 'success', summary: 'x', ...over })
  return store
}

describe('BrowserPanel（0.0.28 驾驶舱）', () => {
  beforeEach(() => {
    teardown()
    pinia = createPinia()
    setActivePinia(pinia)
    shotCalls.calls = []
    shotCalls.b64 = ''
    shotCalls.fail = ''
  })

  it('本会话还没有浏览器动作：URL 栏与截图区都显示空态，不发读图请求', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('随便聊聊')
    const el = await mountPanel()
    expect(el.textContent).toContain('尚未打开页面')
    expect(el.textContent).toContain('暂无截图')
    expect(shotCalls.calls).toEqual([])
  })

  it('有截图：按相对路径拉图并拼 data URL 直进 img', async () => {
    shotCalls.b64 = 'AAAA'
    await seedBrowser({
      callID: 'b1',
      title: 'open http://localhost:5173',
      content: '已打开 http://localhost:5173',
      shot: 's-1/shot-0001.png',
      url: 'http://localhost:5173/',
    })
    const el = await mountPanel()
    await nextTick()
    await nextTick()
    expect(shotCalls.calls).toEqual(['s-1/shot-0001.png']) // shot 字段原样传给绑定
    const img = el.querySelector('img')
    expect(img?.getAttribute('src')).toBe('data:image/png;base64,AAAA')
    expect(el.textContent).toContain('http://localhost:5173/')
  })

  it('跟随最新：新工具事件改写 shot 后自动重取', async () => {
    shotCalls.b64 = 'BBBB'
    const store = await seedBrowser({
      callID: 'b1',
      title: 'open http://x.dev',
      shot: 's-1/shot-0001.png',
      url: 'http://x.dev/',
    })
    const el = await mountPanel()
    await nextTick()
    await nextTick()
    expect(shotCalls.calls).toEqual(['s-1/shot-0001.png'])
    store.onTool({
      sessionID: store.sessionId,
      name: 'browser',
      status: 'success',
      summary: 'x',
      callID: 'b2',
      title: 'click [2]',
      shot: 's-1/shot-0002.jpg',
      url: 'http://x.dev/#next',
    })
    await nextTick()
    await nextTick()
    expect(shotCalls.calls).toEqual(['s-1/shot-0001.png', 's-1/shot-0002.jpg']) // 自动刷新
    expect(el.querySelector('img')?.getAttribute('src')).toBe('data:image/jpeg;base64,BBBB') // .jpg → image/jpeg
  })

  it('读取失败：错误可见并带重试，重试重新发起读图', async () => {
    shotCalls.fail = '截图路径越界（必须在 browser-shots 内）'
    await seedBrowser({ callID: 'b1', title: 'open http://x.dev', shot: 's-1/../evil.png', url: 'http://x.dev/' })
    const el = await mountPanel()
    await nextTick()
    await nextTick()
    expect(el.textContent).toContain('截图路径越界')
    expect(el.querySelector('img')).toBeNull()
    const retry = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.includes('重试'))
    expect(retry).toBeTruthy()
    shotCalls.calls = []
    await retry!.click()
    await nextTick()
    await nextTick()
    expect(shotCalls.calls).toEqual(['s-1/../evil.png'])
  })

  it('控制台尾部逐条渲染；本卡无输出时不渲染控制台区', async () => {
    const store = await seedBrowser({
      callID: 'b1',
      title: 'open http://x.dev',
      shot: 's-1/shot-0001.png',
      url: 'http://x.dev/',
      console: ['10:00:00 [log] ready', '10:00:01 [error] boom'],
    })
    const el = await mountPanel()
    expect(el.textContent).toContain('[log] ready')
    expect(el.textContent).toContain('[error] boom')
    teardown()
    shotCalls.calls = []
    await seedBrowser({ callID: 'b2', title: 'open http://quiet.dev', url: 'http://quiet.dev/' })
    const el2 = await mountPanel()
    expect(el2.textContent).not.toContain('控制台')
  })

  it('元素快照默认折叠，点开后显示 [ref] 元素列表', async () => {
    shotCalls.b64 = 'AAAA'
    await seedBrowser({
      callID: 'b1',
      title: 'open http://x.dev',
      content: '已打开 http://x.dev/\n\n[0] button 登录',
      shot: 's-1/shot-0001.png',
      url: 'http://x.dev/',
    })
    const el = await mountPanel()
    expect(el.textContent).not.toContain('[0] button 登录') // 折叠时不渲染正文
    const toggle = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.includes('元素快照'))
    await toggle!.click()
    await nextTick()
    expect(el.textContent).toContain('[0] button 登录')
  })

  it('执行中的 browser 卡不覆盖画面：显示执行中、保留上一张截图', async () => {
    shotCalls.b64 = 'AAAA'
    const store = await seedBrowser({
      callID: 'b1',
      title: 'open http://x.dev',
      shot: 's-1/shot-0001.png',
      url: 'http://x.dev/',
    })
    const el = await mountPanel()
    await nextTick()
    await nextTick()
    expect(shotCalls.calls).toEqual(['s-1/shot-0001.png'])
    store.onTool({
      sessionID: store.sessionId,
      name: 'browser',
      status: 'running',
      summary: '执行中…',
      callID: 'b3',
    })
    await nextTick()
    expect(el.textContent).toContain('执行中')
    expect(shotCalls.calls).toEqual(['s-1/shot-0001.png']) // running 卡不带 shot：不重取
    expect(el.querySelector('img')?.getAttribute('src')).toBe('data:image/png;base64,AAAA')
  })

  it('头部关闭按钮收起浏览器面板（与 Esc 同路径）', async () => {
    await seedBrowser({ callID: 'b1', title: 'open http://x.dev', url: 'http://x.dev/' })
    const store = useChatStore()
    const el = await mountPanel()
    const close = Array.from(el.querySelectorAll('button')).find((b) => b.title?.includes('关闭'))
    await close!.click()
    expect(store.browserOpen).toBe(false)
  })
})
