import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick } from 'vue'
import RightPanel from './RightPanel.vue'
import type { RightPanelTabDef } from './RightPanel.vue'
import { consumeEsc, registerEsc } from '../composables/useEsc'

// 右栏容器（0.0.28）：tab 化（文件/浏览器），Esc 走消费栈。
// tab 扩展契约 = 注册表（RightPanelTabDef[]）加一项 + 同名插槽 #tab-<id>，容器零改动。

function makeTab(id: string, over: Partial<RightPanelTabDef> = {}): RightPanelTabDef {
  return {
    id,
    label: id === 'file' ? '文件' : '浏览器',
    icon: id === 'file' ? 'file' : 'image',
    active: false,
    activate: vi.fn(),
    close: vi.fn(),
    ...over,
  }
}

let host: HTMLElement
let app: ReturnType<typeof createApp> | null = null

async function mountPanel(tabs: RightPanelTabDef[], slots: Record<string, () => ReturnType<typeof h>>) {
  host = document.createElement('div')
  document.body.appendChild(host)
  app = createApp(defineComponent({ render: () => h(RightPanel, { tabs }, slots) }))
  app.mount(host)
  await nextTick()
  return host
}

function teardown() {
  app?.unmount()
  app = null
  host?.remove()
}

const slots = {
  'tab-file': () => h('p', { class: 'file-content' }, '文件详情内容'),
  'tab-browser': () => h('p', { class: 'browser-content' }, '浏览器内容'),
}

describe('RightPanel（0.0.28 右栏容器）', () => {
  beforeEach(() => {
    teardown()
  })

  it('按注册表渲染 tab 条，激活项 aria-pressed 为真、其余为假', async () => {
    const el = await mountPanel([makeTab('file'), makeTab('browser', { active: true })], slots)
    // 只数 tab 按钮（带 aria-pressed 的）；tab 条右端另有关闭按钮（0.0.25）
    const chips = Array.from(el.querySelectorAll<HTMLButtonElement>('.chip')).filter((b) =>
      b.hasAttribute('aria-pressed'),
    )
    expect(chips).toHaveLength(2)
    expect(chips[0].getAttribute('aria-pressed')).toBe('false')
    expect(chips[1].getAttribute('aria-pressed')).toBe('true')
  })

  it('只渲染激活 tab 的插槽内容（动态插槽 #tab-<id>）', async () => {
    const el = await mountPanel([makeTab('file'), makeTab('browser', { active: true })], slots)
    expect(el.textContent).toContain('浏览器内容')
    expect(el.textContent).not.toContain('文件详情内容')
  })

  it('没有标记激活时落到第一项（store 激活 tab 不在 open 列表时的回退）', async () => {
    const el = await mountPanel([makeTab('file'), makeTab('browser')], slots)
    expect(el.textContent).toContain('文件详情内容')
  })

  it('点击 tab 调它的 activate（store 置激活态，容器不管状态）', async () => {
    const browser = makeTab('browser')
    const el = await mountPanel([makeTab('file', { active: true }), browser], slots)
    const chips = Array.from(el.querySelectorAll<HTMLButtonElement>('.chip')).filter((b) =>
      b.hasAttribute('aria-pressed'),
    )
    await chips[1].click()
    expect(browser.activate).toHaveBeenCalledTimes(1)
  })

  it('Esc 经消费栈关闭激活 tab（close 被调且标记已消费）', async () => {
    const browser = makeTab('browser', { active: true })
    await mountPanel([makeTab('file'), browser], slots)
    expect(consumeEsc()).toBe(true)
    expect(browser.close).toHaveBeenCalledTimes(1)
  })

  it('容器卸载时注销 Esc 消费者（关闭面板后 Esc 归还全局）', async () => {
    await mountPanel([makeTab('file', { active: true })], slots)
    teardown()
    expect(consumeEsc()).toBe(false)
  })

  it('外部注册的 Esc 消费者后进先出：比容器先消费', async () => {
    const outer = vi.fn()
    await mountPanel([makeTab('file', { active: true })], slots)
    const off = registerEsc(outer) // 后于容器注册 = 更晚打开的浮层
    expect(consumeEsc()).toBe(true)
    expect(outer).toHaveBeenCalledTimes(1)
    off()
  })

  it('tab 条右端有关闭按钮（0.0.25 用户反馈：点开后没有关闭入口），点它关激活 tab', async () => {
    const file = makeTab('file')
    const browser = makeTab('browser', { active: true })
    const el = await mountPanel([file, browser], slots)
    const closeBtn = el.querySelector<HTMLButtonElement>('button[aria-label="关闭浏览器面板"]')
    expect(closeBtn).toBeTruthy()
    expect(closeBtn?.getAttribute('title')).toBe('关闭浏览器面板')
    await closeBtn!.click()
    expect(browser.close).toHaveBeenCalledTimes(1)
    expect(file.close).not.toHaveBeenCalled() // 只关激活的那一个
  })

  it('tabs 为空不渲染 tab 条也不抛错（App 层保证不挂空面板，容器兜底）', async () => {
    const el = await mountPanel([], slots)
    expect(el.querySelectorAll('.chip')).toHaveLength(0)
    expect(el.textContent?.trim()).toBe('')
  })
})
