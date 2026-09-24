// @vitest-environment jsdom
// 为什么单独用 jsdom：DOMPurify 依赖完整的 DOM 语义，happy-dom 下会把 pre/h1
// 等块级标签误判为不安全而剥离，导致测试结果与真实 webview 不一致（假红/假绿）。
import { describe, expect, it } from 'vitest'
import { renderMarkdown } from './markdown'

describe('markdown 渲染', () => {
  it('代码块渲染为 pre/code（AI 回复的主要形态）', () => {
    const html = renderMarkdown('```go\nfmt.Println("hi")\n```')
    expect(html).toContain('<pre>')
    expect(html).toContain('<code')
    expect(html).toContain('Println')
  })

  it('行内格式：粗体与行内代码', () => {
    const html = renderMarkdown('**粗** 与 `code`')
    expect(html).toContain('<strong>')
    expect(html).toContain('<code>')
  })

  it('列表与标题', () => {
    const html = renderMarkdown('# 标题\n\n- 一\n- 二')
    expect(html).toContain('<h1')
    expect(html).toContain('<li>')
  })

  // 安全红线：模型输出不可信，脚本与事件属性必须被剥离
  it('消毒：script 与 onerror 一律剥离', () => {
    const html = renderMarkdown('<img src=x onerror=alert(1)><script>alert(2)</script>')
    expect(html).not.toContain('onerror')
    expect(html).not.toContain('<script')
  })

  it('消毒：javascript: 协议链接被剥离', () => {
    const html = renderMarkdown('[点我](javascript:alert(1))')
    expect(html).not.toContain('javascript:')
  })

  it('空输入不炸', () => {
    expect(renderMarkdown('')).toBe('')
  })
})
