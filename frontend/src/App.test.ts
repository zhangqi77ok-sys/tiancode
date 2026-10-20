import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createApp, nextTick, type App } from 'vue'
import { useChatStore } from './stores/chat'

const h = vi.hoisted(() => ({
  memoryLinesCalls: [] as string[],
  memoryLinesResult: {
    global: ['偏好中文回复'],
    project: ['本项目使用 Vue 3'],
  },
  summaries: [
    { id: 'session-mem-1', title: '会话1', workspace: '', lastActiveMs: 1000 },
  ],
  channels: {
    channels: [
      { id: 'ch1', name: '默认渠道', protocol: 'openai', baseUrl: '', model: 'gpt-4o', hasKey: true, active: true },
    ],
    activeId: 'ch1',
  },
}))

vi.mock('./wails', () => ({
  bridge: () => ({
    app: {
      ListSessions: async () => h.summaries.map((s) => s.id),
      ListSessionSummaries: async () => h.summaries,
      ListChannels: async () => h.channels,
      GetWorkspace: async () => '',
      GetTones: async () => ({ mode: 'fixed', default: 'plain', disabled: [], builtin: [] }),
      MemoryLines: async (sessionId: string) => {
        h.memoryLinesCalls.push(sessionId)
        return { global: null as any, project: null as any }
      },
      MemoryDelete: async () => {},
      MemoryClear: async () => {},
      ReplayTail: async () => ({ messages: [], total: 0, hasMore: false }),
      Replay: async () => [],
      ListSkills: async () => [],
      ListMcpServers: async () => [],
    },
    runtime: {
      EventsOn: () => {},
    },
  }),
  errText: (e: unknown) => String(e),
}))

let app: App | null = null
let host: HTMLElement | null = null

describe('App 级集成测试：记忆管理面板链路', () => {
  beforeEach(() => {
    h.memoryLinesCalls = []
    host = document.createElement('div')
    document.body.appendChild(host)
    const pinia = createPinia()
    setActivePinia(pinia)
  })

  afterEach(() => {
    app?.unmount()
    app = null
    host?.remove()
    host = null
    document.body.innerHTML = ''
  })

  it(
    '触发 open-memory 后，aria-label="记忆管理" 的面板真实出现，且 MemoryLines 传入当前 sessionId',
    async () => {
      const AppComp = (await import('./App.vue')).default
      const pinia = createPinia()
      setActivePinia(pinia)
      const store = useChatStore()
      store.sessionId = 'test-session-mem'

      app = createApp(AppComp)
      app.use(pinia)
      app.mount(host!)
      await nextTick()
      await new Promise((r) => setTimeout(r, 50))

      // 确保当前 sessionId 是我们设置的
      store.sessionId = 'test-session-mem'

      // 打开侧栏设置菜单
      const sessionList = host!.querySelector('aside[aria-label="会话列表"]')
      expect(sessionList).toBeTruthy()
      const settingsBtn = sessionList!.querySelector('button[aria-haspopup="menu"]') as HTMLButtonElement
      expect(settingsBtn).toBeTruthy()
      settingsBtn.click()
      await nextTick()

      // 点击设置菜单中的“记忆”
      const menu = host!.querySelector('[role="menu"]')
      expect(menu).toBeTruthy()
      const memoryItem = Array.from(menu!.querySelectorAll('button[role="menuitem"]')).find((b) =>
        b.textContent?.includes('记忆'),
      ) as HTMLButtonElement
      expect(memoryItem).toBeTruthy()
      memoryItem.click()
      await nextTick()
      await new Promise((r) => setTimeout(r, 50))
      await nextTick()

      // 断言 aria-label="记忆管理" 的面板真实出现且能安全展示空态
      const memoryModal = document.querySelector('[aria-label="记忆管理"]')
      expect(memoryModal).toBeTruthy()
      expect(memoryModal?.textContent).toContain('还没有全局记忆')

      // 断言 MemoryLines 被调用，且传入当前 sessionId
      expect(h.memoryLinesCalls).toContain('test-session-mem')
    },
    15000,
  )
})
