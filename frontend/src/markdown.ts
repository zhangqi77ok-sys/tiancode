import { marked } from 'marked'
import DOMPurify from 'dompurify'

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
}

function scrubDangerousHref(token: { type: string; href?: string }): void {
  if ((token.type === 'link' || token.type === 'image') && token.href) {
    if (/^(?:javascript|vbscript|data):/i.test(token.href.trim())) {
      token.href = ''
    }
  }
}

export function renderMarkdown(src: string): string {
  if (!src) return ''
  try {
    // Escape raw HTML so marked does not swallow trailing markdown into an HTML block
    // (e.g. `<script>…</script>[x](javascript:…)`). Scrub dangerous hrefs because
    // happy-dom + DOMPurify does not strip `javascript:` URIs reliably.
    const html = marked.parse(escapeHtml(src), {
      async: false,
      walkTokens: scrubDangerousHref,
    }) as string
    return DOMPurify.sanitize(html, { USE_PROFILES: { html: true } })
  } catch {
    return `<pre>${escapeHtml(src)}</pre>`
  }
}
