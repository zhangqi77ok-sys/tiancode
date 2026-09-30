import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createApp, nextTick, type App } from 'vue'

// 语气面板的状态契约（0.0.18 实机反馈"点『按每条消息自动选』没反应"）：
// 状态本身就写在 aria-pressed 上，而可见的高亮由 style.css 的
// .chip[aria-pressed='true'] 统一负责——组件里用工具栏覆盖 .chip 的颜色是无效的
// （.chip 在 CSS 层之外）。所以这里锁的是"点击必须真的翻转 aria-pressed 这个事实"，
// 一旦有人把状态改成别的表达方式（或忘了翻转），这条用例立刻红。
const h = vi.hoisted(() => ({
  saved: [] as { mode: string; default: string; disabled: string[] }[],
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      GetTones: async () => ({
        mode: 'fixed',
        default: 'plain',
        disabled: [],
        builtin: [
          { id: 'plain', name: '平实', practice: '用最后一条用户消息的语言和详略回答。' },
          { id: 'concise', name: '精简', practice: '先给结论，最多再补一句理由。' },
          { id: 'skeptical', name: '先讲失败', practice: '先写这个做法会在什么情况下失败。' },
        ],
      }),
      SaveTones: async (f: { mode: string; default: string; disabled: string[] }) => {
        // 后端规则：停用默认语气拒绝保存（错误原文要原样显示出来）
        if (f.disabled.includes(f.default)) {
          throw new Error(`默认语气 "${f.default}" 不能停用：请先把默认换成别的语气`)
        }
        h.saved.push(f)
      },
    },
    runtime: { EventsOn: () => {} },
  }),
}))

const ToneSettings = (await import('./ToneSettings.vue')).default

let app: App | null = null

async function mountPanel() {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const pinia = createPinia()
  setActivePinia(pinia)
  app = createApp(ToneSettings)
  app.use(pinia)
  app.mount(host)
  await new Promise((r) => setTimeout(r, 0)) // 等 load() 回来
  await nextTick()
}

// 模态是 Teleport 出去的：在 document 上找
const chipByText = (t: string) =>
  (Array.from(document.querySelectorAll('button.chip')) as HTMLButtonElement[]).find((b) =>
    (b.textContent ?? '').includes(t),
  )
const text = () => document.body.textContent ?? ''
const saveBtn = () =>
  Array.from(document.querySelectorAll('button')).find((b) => (b.textContent ?? '').includes('保存')) as HTMLButtonElement
// 每条语气一行：行内有 [启用/停用] 与 [设为默认] 两个 chip，名字在中间的文本里
const row = (name: string) =>
  Array.from(document.querySelectorAll('div')).find(
    (d) => d.className.includes('items-start') && (d.textContent ?? '').includes(name),
  ) as HTMLElement
const rowChips = (name: string) => Array.from(row(name).querySelectorAll('button.chip')) as HTMLButtonElement[]
const enableChip = (name: string) => rowChips(name)[0]
const defaultChip = (name: string) => rowChips(name)[1]

beforeEach(() => {
  document.body.innerHTML = ''
  h.saved = []
})

afterEach(() => {
  app?.unmount()
  app = null
})

describe('语气面板（状态可见性契约）', () => {
  it('点「按每条消息自动选」必须真的翻转选中态', async () => {
    await mountPanel()
    expect(chipByText('固定一条')?.getAttribute('aria-pressed')).toBe('true')
    expect(chipByText('按每条消息自动选')?.getAttribute('aria-pressed')).toBe('false')

    chipByText('按每条消息自动选')?.click()
    await nextTick()
    expect(chipByText('按每条消息自动选')?.getAttribute('aria-pressed')).toBe('true')
    expect(chipByText('固定一条')?.getAttribute('aria-pressed')).toBe('false')

    // 保存后落到后端（模式确实是自动）
    saveBtn()?.click()
    await new Promise((r) => setTimeout(r, 0))
    expect(h.saved).toHaveLength(1)
    expect(h.saved[0].mode).toBe('auto')
  })

  it('「设为默认」与「已启用」同样翻转 aria-pressed（高亮由样式统一表达）', async () => {
    await mountPanel()
    expect(enableChip('平实')?.getAttribute('aria-pressed')).toBe('true') // 默认那条是启用的
    expect(defaultChip('平实')?.getAttribute('aria-pressed')).toBe('true')

    enableChip('平实')?.click() // 停用一条
    await nextTick()
    expect(enableChip('平实')?.getAttribute('aria-pressed')).toBe('false')

    defaultChip('精简')?.click() // 精简 → 设为默认
    await nextTick()
    expect(text()).toContain('当前默认：精简')
    expect(defaultChip('精简')?.getAttribute('aria-pressed')).toBe('true')
    expect(defaultChip('平实')?.getAttribute('aria-pressed')).toBe('false')
  })

  it('停用默认语气：保存被后端拒绝，错误原文显示出来', async () => {
    await mountPanel()
    enableChip('平实')?.click() // 停用当前默认（plain）
    await nextTick()
    expect(enableChip('平实')?.getAttribute('aria-pressed')).toBe('false')
    saveBtn()?.click()
    await new Promise((r) => setTimeout(r, 0))
    expect(h.saved).toHaveLength(0)
    expect(text()).toContain('不能停用')
  })
})
