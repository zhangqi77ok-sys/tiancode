// 消息渲染：Markdown → 消毒后的 HTML。
// 为什么必须消毒：渲染的是模型输出（不可信）。原始 HTML 不转义成文本（那会让用户
// 看到 <div> 原文），而是交给末端 DOMPurify 白名单——对齐开源实践（Streamdown/open-webui）。
// GFM + breaks:true 是聊天场景惯例：模型输出的单个换行就是换行，不与后续段落黏连。
import { Marked } from 'marked'
import DOMPurify from 'dompurify'

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
}

function scrubDangerousHref(token: { type: string; href?: string }): void {
  if ((token.type === 'link' || token.type === 'image') && token.href) {
    const href = token.href.trim()
    if (
      /^(?:javascript|vbscript):/i.test(href) ||
      /^data:\s*text\/html/i.test(href)
    ) {
      token.href = ''
    }
  }
}

const md = new Marked()
md.use({
  walkTokens(token) {
    scrubDangerousHref(token as { type: string; href?: string })
  },
  tokenizer: {
    // 在闭合标签处截断 script/style/pre/textarea，其后同一行的 markdown 仍会正常词法解析。
    // 为什么必须有：marked 默认把 `[x](javascript:…)` 整行吞进 HTML 块，
    // 上面的 scrubDangerousHref 就够不到那个链接了（删掉它曾让消毒测试红，已实测）。
    html(src: string) {
      const m = /^(<(script|pre|style|textarea)(?=[\s>])[\s\S]*?<\/\2>)/i.exec(src)
      if (!m) return false
      return {
        type: 'html' as const,
        block: true,
        raw: m[0],
        pre: true,
        text: m[0],
      }
    },
  },
})

export function renderMarkdown(src: string): string {
  if (!src) return ''
  try {
    const html = md.parse(src, { async: false, gfm: true, breaks: true }) as string
    return DOMPurify.sanitize(html, {
      USE_PROFILES: { html: true },
      FORBID_TAGS: ['style', 'iframe', 'form'],
    })
  } catch {
    return `<pre>${escapeHtml(src)}</pre>`
  }
}

// 流式期间专用：把未闭合的 ``` 栅栏补上闭合行——半截代码块不能吞掉后续正文
// （Streamdown「unterminated block」同类处理；整段完成后用原文本渲染，所见即最终结果）。
export function closeUnbalancedFences(src: string): string {
  const opens = src.match(/^[ \t]{0,3}```/gm)?.length ?? 0
  return opens % 2 === 1 ? src + '\n```' : src
}
