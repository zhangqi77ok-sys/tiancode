import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import MessageBubble from './MessageBubble.vue'
import type { ChatMsg } from '../stores/chat'
import { consumeEsc } from '../composables/useEsc'

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

  // 深色对比度回归守卫：用户气泡底必须用专用令牌（--c-primary-bubble）而不是
  // --c-primary——深色下主色 #8b74f5 对白字仅 ~3.55:1，正文 AA 需 4.5:1（换深一档
  // #6d55d9 = 5.32:1，数值校验在 style.css 注释里；jsdom 算不了真对比度，这里锁
  // "令牌引用不回退"，防止样式改回直连主色）。
  it('用户气泡底色走专用对比度令牌（深色 AA 守卫）', () => {
    const el = mount(userMsg)
    const bubble = el.querySelector('div.whitespace-pre-wrap') as HTMLElement | null
    expect(bubble, '用户正文气泡要渲染').toBeTruthy()
    expect(bubble?.className).toContain('bg-[var(--c-primary-bubble)]')
    expect(bubble?.className).not.toContain('bg-[var(--c-primary)]')
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
    // 正文照旧渲染（附件行与正文是相邻两块，都不少）
    expect(el.textContent ?? '').toContain('写的什么内容')
  })

  // 0.0.25：每个附件都是**可点的名字**（此前文件是 span，点不了），点开本机详情
  it('点附件名字打开本机详情（文件名/类型/内联方式/路径）', async () => {
    const el = mount({
      id: 'u10',
      role: 'user',
      content: '写的什么内容',
      at: Date.now(),
      attachments: [
        { kind: 'file', name: '#1065.txt', mediaType: 'text/plain', path: 'att/s/file-1-#1065.txt', inline: 'full' },
      ],
    })
    const chip = el.querySelector('[title*="#1065.txt"]') as HTMLButtonElement | null
    expect(chip?.tagName, '附件名字必须是可点控件').toBe('BUTTON')
    chip?.click()
    await nextTick()
    const text = document.body.textContent ?? ''
    expect(text).toContain('#1065.txt')
    expect(text).toContain('文件 · text/plain')
    expect(text).toContain('已内联（内容随请求一起发给模型）')
    expect(text).toContain('att/s/file-1-#1065.txt')
    // Esc 关详情：走浮层消费栈（不打断回合）
    expect(consumeEsc()).toBe(true)
    await nextTick()
    expect(document.body.textContent ?? '').not.toContain('内联方式')
  })

  // 0.0.25：图片没有 dataUrl（附件已不在本机）时也要留名字——此前整张图和名字一起消失
  it('图片附件无 dataUrl 仍留名字，图标替代缩略图', () => {
    const el = mount({
      id: 'u11',
      role: 'user',
      content: '看截图',
      at: Date.now(),
      attachments: [{ kind: 'image', name: 'shot.png', mediaType: 'image/png', path: 'att/s/shot.png', inline: 'full' }],
    })
    const chip = el.querySelector('[title*="shot.png"]') as HTMLElement | null
    expect(chip, '图片附件也要有 chip').toBeTruthy()
    expect(chip?.textContent).toContain('shot.png')
    expect(el.querySelector('img'), '没有 dataUrl 就不出缩略图').toBeFalsy()
  })

  // 0.0.25：图片有 dataUrl 时名字与缩略图并存，点名字开详情（点缩略图仍是放大）
  it('图片有 dataUrl：名字 + 缩略图并存', async () => {
    const el = mount({
      id: 'u12',
      role: 'user',
      content: '看截图',
      at: Date.now(),
      attachments: [
        { kind: 'image', name: 'shot.png', mediaType: 'image/png', dataUrl: 'data:image/png;base64,AAA', path: 'att/s/shot.png', inline: 'full' },
      ],
    })
    const thumb = el.querySelector('img') as HTMLImageElement | null
    expect(thumb?.getAttribute('src')).toContain('data:image/png;base64,AAA')
    const chip = thumb?.closest('button') as HTMLButtonElement | null
    expect(chip?.textContent).toContain('shot.png')
    chip?.click()
    await nextTick()
    // 详情里用现成的 dataUrl 显示原图
    const imgs = document.body.querySelectorAll('img')
    expect(imgs.length).toBeGreaterThanOrEqual(2)
    expect(document.body.textContent ?? '').toContain('图片 · image/png')
  })
})
