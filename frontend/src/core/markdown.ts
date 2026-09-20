import { marked } from 'marked'
import hljs from 'highlight.js'

marked.setOptions({
  gfm: true,
  breaks: true,
})

marked.use({
  renderer: {
    code({ text, lang }: { text: string; lang?: string }) {
      const validLang = lang && hljs.getLanguage(lang) ? lang : 'plaintext'
      let highlighted = text
      try {
        highlighted = hljs.highlight(text, { language: validLang, ignoreIllegals: true }).value
      } catch {
        highlighted = text
      }
      const encodedCode = encodeURIComponent(text)
      return `<div class="code-block-card my-3 rounded-xl overflow-hidden border border-black/[0.1] bg-[#1E1C1A] text-[#F4F4F5] shadow-xs select-text font-mono text-xs">
  <div class="flex items-center justify-between px-3 py-1.5 bg-[#2A2724] border-b border-white/[0.08] text-[11px] text-[#A1A1AA] select-none">
    <span class="font-bold text-[#D96B27] uppercase tracking-wider text-[10px]">${validLang}</span>
    <div class="flex items-center gap-1.5">
      <button class="code-copy-btn px-2 py-0.5 rounded hover:bg-white/10 hover:text-white transition-all cursor-pointer flex items-center gap-1 text-[10px]" data-code="${encodedCode}" title="复制此段代码到剪贴板">
        <span>📋</span><span>复制</span>
      </button>
      <button class="code-apply-btn px-2 py-0.5 rounded hover:bg-white/10 hover:text-emerald-400 transition-all cursor-pointer flex items-center gap-1 text-[10px]" data-code="${encodedCode}" title="将此代码注入当前编辑器">
        <span>⚡</span><span>应用至编辑器</span>
      </button>
    </div>
  </div>
  <pre class="p-3 overflow-x-auto leading-relaxed text-[11px]"><code class="hljs language-${validLang}">${highlighted}</code></pre>
</div>`
    }
  }
})

const markdownCache = new Map<string, string>()
const MAX_CACHE_SIZE = 500

export function renderMarkdown(content: string): string {
  if (!content) return ''
  const cached = markdownCache.get(content)
  if (cached !== undefined) {
    return cached
  }
  try {
    const rendered = marked.parse(content) as string
    if (markdownCache.size >= MAX_CACHE_SIZE) {
      const firstKey = markdownCache.keys().next().value
      if (firstKey) markdownCache.delete(firstKey)
    }
    markdownCache.set(content, rendered)
    return rendered
  } catch {
    return content
  }
}
