import DOMPurify from 'dompurify'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { closeUnbalancedFences, renderMarkdown } from './markdown'

describe('renderMarkdown', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('渲染代码块与粗体', () => {
    const html = renderMarkdown('**hi**\n\n```go\nfmt.Println(1)\n```')
    expect(html).toContain('<strong>')
    expect(html).toContain('<pre>')
    expect(html).toContain('fmt.Println')
  })

  it('去掉 script 与 javascript URL', () => {
    const sanitize = vi.spyOn(DOMPurify, 'sanitize')
    const html = renderMarkdown('<script>alert(1)</script>[x](javascript:alert(1))')
    expect(sanitize).toHaveBeenCalled()
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

  // 模型输出的单个换行就是换行（聊天惯例；不开 breaks 时段落会黏连）
  it('单个换行渲染为换行', () => {
    expect(renderMarkdown('第一行\n第二行')).toContain('<br')
  })

  // 代码块带语言标签与复制按钮（开源聊天 UI 标配），按钮须经消毒存活
  it('代码块带语言标签与复制按钮', () => {
    const html = renderMarkdown('```go\nx := 1\n```')
    expect(html).toContain('code-lang">go</span>')
    expect(html).toContain('data-copy')
    expect(html).toContain('<pre><code>x := 1</code></pre>')
  })

  // 原始 HTML 过白名单后渲染，而不是转义成文本（转义会让用户看到 <div> 原文）
  it('白名单内 HTML 渲染、危险标签剥除', () => {
    const html = renderMarkdown('<b>加粗</b><script>alert(1)</script>')
    expect(html).toContain('<b>加粗</b>')
    expect(html.toLowerCase()).not.toContain('<script')
  })
})

describe('closeUnbalancedFences', () => {
  it('奇数栅栏补闭合行', () => {
    expect(closeUnbalancedFences('```go\nx := 1')).toBe('```go\nx := 1\n```')
  })

  it('成对栅栏原样返回', () => {
    const s = '```go\nx := 1\n```'
    expect(closeUnbalancedFences(s)).toBe(s)
  })

  it('行内三反引号不计数（仅识别行首栅栏）', () => {
    const s = '用 ` ``` ` 表示栅栏\n\n```go\nx := 1\n```'
    expect(closeUnbalancedFences(s)).toBe(s)
  })
})
