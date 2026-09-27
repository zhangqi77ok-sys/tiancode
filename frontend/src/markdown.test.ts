import { describe, expect, it } from 'vitest'
import { renderMarkdown } from './markdown'

describe('renderMarkdown', () => {
  it('渲染代码块与粗体', () => {
    const html = renderMarkdown('**hi**\n\n```go\nfmt.Println(1)\n```')
    expect(html).toContain('<strong>')
    expect(html).toContain('<pre>')
    expect(html).toContain('fmt.Println')
  })

  it('去掉 script 与 javascript URL', () => {
    const html = renderMarkdown('<script>alert(1)</script>[x](javascript:alert(1))')
    expect(html.toLowerCase()).not.toContain('<script')
    expect(html.toLowerCase()).not.toContain('javascript:')
  })

  it('代码块内比较符不双重转义', () => {
    const html = renderMarkdown('```go\nif a < b {\n}\n```')
    expect(html).not.toContain('&amp;lt;')
    expect(html).toContain('a &lt; b')
    expect(html).toMatch(/<(?:pre|code)\b/i)
  })

  it('空输入返回空串', () => {
    expect(renderMarkdown('')).toBe('')
  })
})
