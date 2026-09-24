// 消息渲染：Markdown → 消毒后的 HTML。
//
// 为什么必须消毒：渲染的是**模型输出**（不可信内容）。直接 v-html 等于把 XSS
// 交给上游模型或中转站——一次带 <img onerror> 的回复就能执行脚本。
// 所以链路固定为 marked 解析 → DOMPurify 消毒 → v-html。
import DOMPurify from 'dompurify'
import { marked } from 'marked'

marked.setOptions({ gfm: true, breaks: true })

// renderMarkdown 把 Markdown 文本渲染为可安全插入的 HTML 字符串。
export function renderMarkdown(src: string): string {
  if (!src) return ''
  const html = marked.parse(src, { async: false }) as string
  return DOMPurify.sanitize(html, { USE_PROFILES: { html: true } })
}
