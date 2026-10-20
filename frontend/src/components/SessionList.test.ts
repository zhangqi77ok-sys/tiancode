import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createApp, defineComponent, h, nextTick, ref, type App } from 'vue'
import SessionList from './SessionList.vue'
import ShortcutsOverlay from './ShortcutsOverlay.vue'
import MemoryPanel from './MemoryPanel.vue'
import { useChatStore } from '../stores/chat'
import { consumeEsc } from '../composables/useEsc'

const memoryCalls: string[] = []

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ListSessions: async () => [],
      ListSessionSummaries: async () => [],
      ListChannels: async () => ({ channels: [], activeId: '' }),
      GetWorkspace: async () => '',
      GetTones: async () => ({ mode: 'fixed', default: 'plain', disabled: [], builtin: [] }),
      MemoryLines: async (sessionId: string) => {
        memoryCalls.push(sessionId)
        return { global: null as any, project: null as any }
      },
    },
    runtime: { EventsOn: () => {} },
  }),
}))

let app: App | null = null
let host: HTMLElement | null = null

describe('SessionList 设置菜单新增项（0.0.45 任务 5, 7, 9）', () => {
  beforeEach(() => {
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

  it('点击设置菜单「审批与权限」→ 触发 open-approval-settings 事件', async () => {
    let approvalOpened = false
    app = createApp(SessionList, {
      onOpenApprovalSettings: () => {
        approvalOpened = true
      },
    })
    app.use(createPinia())
    app.mount(host!)
    await nextTick()

    // 打开设置菜单
    const settingsBtn = host!.querySelector('button[aria-haspopup="menu"]') as HTMLButtonElement
    expect(settingsBtn).toBeTruthy()
    settingsBtn.click()
    await nextTick()

    const menu = host!.querySelector('[role="menu"]')
    expect(menu).toBeTruthy()

    const item = Array.from(menu!.querySelectorAll('button[role="menuitem"]')).find((b) =>
      b.textContent?.includes('审批与权限'),
    ) as HTMLButtonElement
    expect(item).toBeTruthy()
    item.click()
    await nextTick()

    expect(approvalOpened).toBe(true)
  })

  it('点击设置菜单「快捷键」→ 快捷键浮层出现；Esc 关闭（Task 7 验收）', async () => {
    // 搭建集成宿主：包含 SessionList 与 ShortcutsOverlay
    const shortcutsOpen = ref(false)
    const Harness = defineComponent({
      setup() {
        return () =>
          h('div', [
            h(SessionList, {
              onOpenShortcuts: () => {
                shortcutsOpen.value = true
              },
            }),
            shortcutsOpen.value ? h(ShortcutsOverlay, { onClose: () => { shortcutsOpen.value = false } }) : null,
          ])
      },
    })

    app = createApp(Harness)
    app.use(createPinia())
    app.mount(host!)
    await nextTick()

    // 初始浮层未出现
    expect(document.querySelector('[aria-label="快捷键速查"]')).toBeNull()

    // 打开设置菜单
    const settingsBtn = host!.querySelector('button[aria-haspopup="menu"]') as HTMLButtonElement
    settingsBtn.click()
    await nextTick()

    const menu = host!.querySelector('[role="menu"]')
    expect(menu).toBeTruthy()

    const item = Array.from(menu!.querySelectorAll('button[role="menuitem"]')).find((b) =>
      b.textContent?.includes('快捷键'),
    ) as HTMLButtonElement
    expect(item).toBeTruthy()
    // 右侧灰字显示 ?
    expect(item.textContent).toContain('?')

    // 点击打开快捷键浮层
    item.click()
    await nextTick()

    // 快捷键浮层出现
    expect(shortcutsOpen.value).toBe(true)
    const overlay = document.querySelector('[aria-label="快捷键速查"]')
    expect(overlay).toBeTruthy()

    // Esc 关闭
    expect(consumeEsc()).toBe(true)
    await nextTick()
    expect(shortcutsOpen.value).toBe(false)
    expect(document.querySelector('[aria-label="快捷键速查"]')).toBeNull()
  })

  it('点击设置菜单「关于 tiancode」→ 触发 open-about 事件', async () => {
    let aboutOpened = false
    app = createApp(SessionList, {
      onOpenAbout: () => {
        aboutOpened = true
      },
    })
    app.use(createPinia())
    app.mount(host!)
    await nextTick()

    const settingsBtn = host!.querySelector('button[aria-haspopup="menu"]') as HTMLButtonElement
    settingsBtn.click()
    await nextTick()

    const menu = host!.querySelector('[role="menu"]')
    const item = Array.from(menu!.querySelectorAll('button[role="menuitem"]')).find((b) =>
      b.textContent?.includes('关于 tiancode'),
    ) as HTMLButtonElement
    expect(item).toBeTruthy()
    item.click()
    await nextTick()

    expect(aboutOpened).toBe(true)
  })

  it('点击设置菜单「记忆」→ 触发 open-memory 事件', async () => {
    let memoryOpened = false
    app = createApp(SessionList, {
      onOpenMemory: () => {
        memoryOpened = true
      },
    })
    app.use(createPinia())
    app.mount(host!)
    await nextTick()

    const settingsBtn = host!.querySelector('button[aria-haspopup="menu"]') as HTMLButtonElement
    expect(settingsBtn).toBeTruthy()
    settingsBtn.click()
    await nextTick()

    const menu = host!.querySelector('[role="menu"]')
    expect(menu).toBeTruthy()

    const item = Array.from(menu!.querySelectorAll('button[role="menuitem"]')).find((b) =>
      b.textContent?.includes('记忆'),
    ) as HTMLButtonElement
    expect(item).toBeTruthy()
    item.click()
    await nextTick()

    expect(memoryOpened).toBe(true)
  })

  it('集成测试：触发 open-memory → 记忆管理面板出现，且 MemoryLines 传入当前 sessionId', async () => {
    memoryCalls.length = 0
    const memoryOpen = ref(false)

    const Harness = defineComponent({
      setup() {
        return () =>
          h('div', [
            h(SessionList, {
              onOpenMemory: () => {
                memoryOpen.value = true
              },
            }),
            memoryOpen.value ? h(MemoryPanel, { onClose: () => { memoryOpen.value = false } }) : null,
          ])
      },
    })

    const pinia = createPinia()
    setActivePinia(pinia)
    const store = useChatStore()
    store.sessionId = 'test-session-integration'

    app = createApp(Harness)
    app.use(pinia)
    app.mount(host!)
    await nextTick()

    // 初始面板未出现
    expect(document.querySelector('[aria-label="记忆管理"]')).toBeNull()

    // 打开设置菜单
    const settingsBtn = host!.querySelector('button[aria-haspopup="menu"]') as HTMLButtonElement
    settingsBtn.click()
    await nextTick()

    const menu = host!.querySelector('[role="menu"]')
    const item = Array.from(menu!.querySelectorAll('button[role="menuitem"]')).find((b) =>
      b.textContent?.includes('记忆'),
    ) as HTMLButtonElement
    item.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 50))
    await nextTick()

    // 断言 aria-label="记忆管理" 的面板真实出现
    expect(memoryOpen.value).toBe(true)
    const modal = document.querySelector('[aria-label="记忆管理"]')
    expect(modal).toBeTruthy()
    expect(modal?.textContent).toContain('还没有全局记忆')

    // 断言 MemoryLines 被调用，且传入当前 sessionId
    expect(memoryCalls).toContain('test-session-integration')
  })
})
