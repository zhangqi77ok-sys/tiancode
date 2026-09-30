import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, ref, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { renderMarkdown } from '../markdown'
import MarkdownBody from './MarkdownBody.vue'

// 阶段 2：① 未闭合栅栏先用纯文本，闭合后才高亮（且只高亮那一次）；
// ② 终态渲染与"全文渲染"逐字一致（所见即最终结果）；
// ③ 流式期间 300ms 合帧、终态立即渲染（不等下一个帧）。
vi.mock('../wails', () => ({
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
