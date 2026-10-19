import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createApp, defineComponent, h, nextTick, ref, type App } from 'vue'
import SessionList from './SessionList.vue'
import ShortcutsOverlay from './ShortcutsOverlay.vue'
import { consumeEsc } from '../composables/useEsc'

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ListSessions: async () => [],
      ListSessionSummaries: async () => [],
      ListChannels: async () => ({ channels: [], activeId: '' }),
      GetWorkspace: async () => '',
      GetTones: async () => ({ mode: 'fixed', default: 'plain', disabled: [], builtin: [] }),
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
})
