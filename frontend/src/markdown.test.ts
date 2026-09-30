import DOMPurify from 'dompurify'
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  clearRenderCache,
  closeUnbalancedFences,
  renderCacheStats,
  renderMarkdown,
  unclosedCodeFrom,
} from './markdown'

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
    expect(html).toContain('class="hljs language-go"')
  })

  // 语法高亮：已注册语言输出 hljs 标记（AI 编程工具的代码可读性基线）
  it('已注册语言的代码块带语法高亮', () => {
    const html = renderMarkdown('```json\n{"a": 1}\n```')
    expect(html).toContain('hljs-attr')
    expect(html).toContain('language-json')
  })

  it('未注册语言回退转义、无高亮标记', () => {
    const html = renderMarkdown('```weirdlang\n<x>\n```')
    expect(html).toContain('&lt;x&gt;')
    expect(html).not.toContain('hljs-attr')
  })

  // 原始 HTML 过白名单后渲染，而不是转义成文本（转义会让用户看到 <div> 原文）
  it('白名单内 HTML 渲染、危险标签剥除', () => {
    const html = renderMarkdown('<b>加粗</b><script>alert(1)</script>')
    expect(html).toContain('<b>加粗</b>')
    expect(html.toLowerCase()).not.toContain('<script')
  })
})

// 阶段 2：渲染结果缓存——流式增量会让整张列表重渲染，已完成消息不能反复重解析
describe('渲染缓存（阶段 2）', () => {
  it('同一内容只解析一次，两次拿到同一份 HTML', () => {
    clearRenderCache()
    const src = '```go\nfunc main() {}\n```'
    const first = renderMarkdown(src)
    const second = renderMarkdown(src)
    expect(second).toBe(first)
    expect(renderCacheStats().entries).toBe(1)
  })

  it('未闭合与闭合是两份结果，不能互相顶掉', () => {
    clearRenderCache()
    const src = '```json\n{"a": 1}\n```'
    const lit = renderMarkdown(src)
    const plain = renderMarkdown(src, { plainCodeFrom: 0 })
    expect(plain).not.toBe(lit)
    expect(renderCacheStats().entries).toBe(2)
  })

  it('缓存有上限，不随长会话无限长大', () => {
    clearRenderCache()
    for (let i = 0; i < 420; i++) renderMarkdown(`第 ${i} 段`)
    expect(renderCacheStats().entries).toBeLessThanOrEqual(400)
  })
})

// 阶段 2：正在输入的那个代码块先用纯文本，闭合后再高亮一次
describe('未闭合栅栏先用纯文本（阶段 2）', () => {
  it('unclosedCodeFrom 只数行首栅栏', () => {
    expect(unclosedCodeFrom('正文')).toBeUndefined()
    expect(unclosedCodeFrom('```go\nx := 1\n```')).toBeUndefined()
    expect(unclosedCodeFrom('```go\nx := 1')).toBe(0)
    expect(unclosedCodeFrom('```go\na\n```\n\n```go\nb')).toBe(1)
  })

  it('未闭合的那块不高亮；闭合后（下一帧）才高亮', () => {
    const partial = '```json\n{"a": 1}\n' // 正在输入：栅栏未闭合
    const plain = renderMarkdown(closeUnbalancedFences(partial), { plainCodeFrom: unclosedCodeFrom(partial) })
    expect(plain).toContain('language-json')
    expect(plain).not.toContain('hljs-attr') // 纯文本，无高亮标记

    const closed = '```json\n{"a": 1}\n```'
    expect(renderMarkdown(closed)).toContain('hljs-attr')
  })

  it('已闭合的块在流式期间照常高亮（只让最后那块保持纯文本）', () => {
    const src = '```json\n{"a": 1}\n```\n\n```go\nfunc main() {'
    const html = renderMarkdown(closeUnbalancedFences(src), { plainCodeFrom: unclosedCodeFrom(src) })
    expect(html).toContain('hljs-attr') // 第一块已闭合：高亮
    expect(html).not.toContain('hljs-keyword') // 第二块在输入：纯文本
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
