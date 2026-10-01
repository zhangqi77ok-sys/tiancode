import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, ref, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { renderMarkdown } from '../markdown'
import { useChatStore } from '../stores/chat'
import MarkdownBody from './MarkdownBody.vue'

// 阶段 2：① 未闭合栅栏先用纯文本，闭合后才高亮（且只高亮那一次）；
// ② 终态渲染与"全文渲染"逐字一致（所见即最终结果）；
// ③ 流式期间 300ms 合帧、终态立即渲染（不等下一个帧）。
const mw = vi.hoisted(() => ({ externalOpens: [] as string[] }))

vi.mock('../wails', () => ({
  openExternal: (url: string) => {
    mw.externalOpens.push(url)
  },
  bridge: () => ({ app: { Stop: async () => {} }, runtime: { EventsOn: () => {} } }),
}))

const content = ref('')
const streaming = ref(false)
let app: App | null = null

function mount(): HTMLElement {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const pinia = createPinia()
  setActivePinia(pinia)
  app = createApp(
    defineComponent({
      render: () => h(MarkdownBody, { content: content.value, streaming: streaming.value }),
    }),
  )
  app.use(pinia)
  app.mount(host)
  return host.querySelector('.markdown-body') as HTMLElement
}

const waitFrame = () => new Promise((r) => setTimeout(r, 380)) // 越过 300ms 合帧窗口

afterEach(() => {
  app?.unmount()
  app = null
  document.body.innerHTML = ''
})

describe('MarkdownBody（阶段 2 流式渲染）', () => {
  it('未闭合栅栏先用纯文本，闭合后高亮', async () => {
    content.value = '```go\nfunc main() {'
    streaming.value = true
    const el = mount()
    expect(el.innerHTML).toContain('func main()')
    expect(el.innerHTML).not.toContain('hljs-keyword') // 正在输入：纯文本

    content.value = '```go\nfunc main() {}\n```'
    await waitFrame()
    await nextTick()
    expect(el.innerHTML).toContain('hljs-keyword') // 闭合后高亮一次
  })

  it('终态渲染与全文渲染逐字一致（所见即最终结果）', async () => {
    const partial = '先给结论，再补一段代码：\n\n```go\nfunc main() {'
    content.value = partial
    streaming.value = true
    const el = mount()
    await waitFrame()
    await nextTick()

    const full = '先给结论，再补一段代码：\n\n```go\nfunc main() {}\n```\n\n就这些。'
    content.value = full
    streaming.value = false // 终态：立即渲染，不等合帧
    await nextTick()
    expect(el.innerHTML).toBe(renderMarkdown(full))
  })

  it('流式期间增量合帧：新内容不立刻渲染，等下一帧', async () => {
    content.value = '第一段'
    streaming.value = true
    const el = mount()
    expect(el.innerHTML).toContain('第一段')

    content.value = '第一段\n\n**加粗**'
    await nextTick()
    expect(el.innerHTML).not.toContain('<strong>') // 还在合帧窗口内

    await waitFrame()
    await nextTick()
    expect(el.innerHTML).toContain('<strong>')
  })
})

// 点链接（0.0.29 bug 根治）：链接的默认行为会把整个应用窗口导航走（WebView 没有
// 地址栏和后退，用户被困在目标页面）——必须 preventDefault 并按 href 分类处置：
// http/https 进驾驶舱；#锚点滚页内；mailto 等交系统浏览器；相对链接忽略。
describe('MarkdownBody 链接点击', () => {
  beforeEach(() => {
    mw.externalOpens = []
  })

  function clickLink(el: HTMLElement): MouseEvent {
    const a = el.querySelector('a') as HTMLAnchorElement
    expect(a).toBeTruthy()
    const evt = new MouseEvent('click', { bubbles: true, cancelable: true })
    a.dispatchEvent(evt)
    return evt
  }

  it('点 http 链接：阻止默认导航，URL 交给 store.openLinkInBrowser', async () => {
    content.value = '[示例](https://example.com)'
    streaming.value = false
    const el = mount()
    await nextTick()
    const store = useChatStore()
    const spy = vi.fn()
    store.openLinkInBrowser = spy
    const evt = clickLink(el)
    expect(evt.defaultPrevented).toBe(true)
    expect(spy).toHaveBeenCalledWith('https://example.com')
    expect(mw.externalOpens).toEqual([]) // http/https 不直接进系统浏览器
  })

  it('页内锚点：滚到对应元素，不进驾驶舱也不进系统浏览器', async () => {
    content.value = '[跳到章节](#sec-1)'
    streaming.value = false
    const el = mount()
    await nextTick()
    // marked 渲染不出目标元素：往正文里塞一个 id 节点当滚动目标
    const target = document.createElement('div')
    target.id = 'sec-1'
    const scrollSpy = vi.fn()
    ;(target as unknown as { scrollIntoView: unknown }).scrollIntoView = scrollSpy
    el.appendChild(target)
    const store = useChatStore()
    const spy = vi.fn()
    store.openLinkInBrowser = spy
    const evt = clickLink(el)
    expect(evt.defaultPrevented).toBe(true)
    expect(scrollSpy).toHaveBeenCalledTimes(1) // 页内滚动
    expect(spy).not.toHaveBeenCalled()
    expect(mw.externalOpens).toEqual([])
  })

  it('mailto 链接直接交系统浏览器，不绕道驾驶舱', async () => {
    content.value = '[写信](mailto:a@b.com)'
    streaming.value = false
    const el = mount()
    await nextTick()
    const store = useChatStore()
    const spy = vi.fn()
    store.openLinkInBrowser = spy
    const evt = clickLink(el)
    expect(evt.defaultPrevented).toBe(true)
    expect(mw.externalOpens).toEqual(['mailto:a@b.com'])
    expect(spy).not.toHaveBeenCalled() // BrowserNavigate 收不了 mailto，别去碰后端
  })

  it('相对链接：只拦截不动作——没有可靠解析基准，宁可不开垃圾 URL', async () => {
    content.value = '[文档](./docs/readme.md)'
    streaming.value = false
    const el = mount()
    await nextTick()
    const store = useChatStore()
    const spy = vi.fn()
    store.openLinkInBrowser = spy
    const evt = clickLink(el)
    expect(evt.defaultPrevented).toBe(true)
    expect(spy).not.toHaveBeenCalled()
    expect(mw.externalOpens).toEqual([])
  })
})
