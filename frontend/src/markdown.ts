import { Marked, type Tokens } from 'marked'
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
  renderer: {
    html({ text }: Tokens.HTML | Tokens.Tag) {
      return escapeHtml(text)
    },
  },
  tokenizer: {
    // End script/style/pre/textarea at the closing tag so trailing markdown still lexes
    // (default marked keeps `[x](javascript:…)` inside the HTML block).
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
    const html = md.parse(src, { async: false }) as string
    return DOMPurify.sanitize(html, { USE_PROFILES: { html: true } })
  } catch {
    return `<pre>${escapeHtml(src)}</pre>`
  }
}
