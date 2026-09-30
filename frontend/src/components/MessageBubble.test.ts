import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import MessageBubble from './MessageBubble.vue'
import type { ChatMsg } from '../stores/chat'

// 第 7 批：① 删掉「重试上一问」（它只把上一问原文再发一遍，失败回合仍留在流里）；
// ② 用户气泡有「复制」；③ 「重跑」只把原文 + 附件交回输入框，绝不自己重发。

const h2 = vi.hoisted(() => ({ sends: [] as string[], rerun: [] as unknown[] }))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      Send: async (_sid: string, text: string) => {
        h2.sends.push(text)
      },
      Stop: async () => {},
    },
    runtime: { EventsOn: () => {} },
  }),
  winClose: async () => {},
  winMinimize: async () => {},
  winToggleMaximize: async () => {},
}))

let host: HTMLElement
let app: ReturnType<typeof createApp> | null = null

function mount(m: ChatMsg, run?: ChatMsg[]): HTMLElement {
  host = document.createElement('div')
  document.body.appendChild(host)
  const pinia = createPinia()
  app = createApp(
    defineComponent({
      render: () => h(MessageBubble, { m, run, onRerun: (p: unknown) => h2.rerun.push(p) }),
    }),
  )
  app.use(pinia)
  setActivePinia(pinia)
  app.mount(host)
  return host
}

function teardown() {
  app?.unmount()
  app = null
  host?.remove()
}

const userMsg: ChatMsg = {
  id: 'u1',
  role: 'user',
  content: '把 agent.go 的这个函数改一下',
  seq: 7,
  at: Date.now(),
  attachments: [
    { kind: 'image', name: 'shot.png', mediaType: 'image/png', dataUrl: 'data:image/png;base64,AAA', inline: 'none' },
  ],
}

describe('MessageBubble（第 7 批）', () => {
  beforeEach(() => {
    teardown()
    h2.sends = []
    h2.rerun = []
  })

  it('用户气泡「重跑」只回填输入框，不发消息', async () => {
    const el = mount(userMsg)
    const btn = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.includes('重跑'))
    expect(btn).toBeTruthy()
    ;(btn as HTMLButtonElement).click()
    await nextTick()
    // 只发出一个"回填"事件（原文 + 原附件），绝不调用只发原文的 Send
    expect(h2.rerun).toHaveLength(1)
    expect(h2.rerun[0]).toMatchObject({ seq: 7, text: '把 agent.go 的这个函数改一下' })
    expect((h2.rerun[0] as { attachments: unknown[] }).attachments).toHaveLength(1)
    expect(h2.sends).toEqual([])
  })

  it('用户气泡「复制」写剪贴板', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const el = mount(userMsg)
    const btn = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.includes('复制'))
    expect(btn).toBeTruthy()
    ;(btn as HTMLButtonElement).click()
    await nextTick()
    expect(writeText).toHaveBeenCalledWith('把 agent.go 的这个函数改一下')
  })

  it('出错的助手回合不再有「重试上一问」', () => {
    const el = mount({
      id: 'a1',
      role: 'assistant',
      content: '⚠ 出错了：无可用渠道',
      error: true,
      at: Date.now(),
    })
    expect(el.textContent ?? '').not.toContain('重试上一问')
  })

  // 0.0.21 用户给的样式：文件附件是内联小 chip（图标 + 文件名），不是一行一个方框
  it('文件附件渲染成内联 chip，内联状态进 title', () => {
    const el = mount({
      id: 'u9',
      role: 'user',
      content: '写的什么内容',
      at: Date.now(),
      attachments: [
        { kind: 'file', name: '#1065.txt', path: 'att/s/file-1-#1065.txt', inline: 'full' },
      ],
    })
    const chip = el.querySelector('[title*="#1065.txt"]') as HTMLElement | null
    expect(chip, '附件要有可 hover 的 chip').toBeTruthy()
    expect(chip?.textContent).toContain('#1065.txt')
    expect(chip?.getAttribute('title')).toContain('已内联')
    expect(chip?.getAttribute('title')).toContain('att/s/file-1-#1065.txt')
    // 正文照旧渲染（chip 与正文同排，不再各自占一行）
    expect(el.textContent ?? '').toContain('写的什么内容')
    expect(chip?.parentElement?.textContent ?? '').toContain('写的什么内容')
  })
})
