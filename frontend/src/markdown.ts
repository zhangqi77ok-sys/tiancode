// 消息渲染：Markdown → 消毒后的 HTML。
// 为什么必须消毒：渲染的是模型输出（不可信）。原始 HTML 不转义成文本（那会让用户
// 看到 <div> 原文），而是交给末端 DOMPurify 白名单——对齐开源实践（Streamdown/open-webui）。
// GFM + breaks:true 是聊天场景惯例：模型输出的单个换行就是换行，不与后续段落黏连。
import { Marked, type Tokens } from 'marked'
import DOMPurify from 'dompurify'
import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import diff from 'highlight.js/lib/languages/diff'
import go from 'highlight.js/lib/languages/go'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import markdown from 'highlight.js/lib/languages/markdown'
import python from 'highlight.js/lib/languages/python'
import sql from 'highlight.js/lib/languages/sql'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'

// 按需注册 AI 编程工具最高频的语言（tree-shake 后只打包这些，不引全量 hljs）。
// 语法高亮是 AI 编程工具的立身之本——纯文本代码块可读性不达标（用户 0.2.5 反馈）。
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('diff', diff)
hljs.registerLanguage('go', go)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('json', json)
hljs.registerLanguage('markdown', markdown)
hljs.registerLanguage('python', python)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('yaml', yaml)
// 常见别名
hljs.registerAliases(['ts'], { languageName: 'typescript' })
hljs.registerAliases(['js'], { languageName: 'javascript' })
hljs.registerAliases(['sh', 'shell', 'powershell'], { languageName: 'bash' })
hljs.registerAliases(['yml'], { languageName: 'yaml' })
hljs.registerAliases(['html'], { languageName: 'xml' })

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
    // 代码块包装：语言标签 + 复制按钮（开源聊天 UI 标配）。
    // button/class/data-* 均在 DOMPurify 白名单内，能过末端消毒；点击由 MarkdownBody 事件委托处理。
    code({ text, lang }: Tokens.Code): string {
      const label = (lang || '').trim().split(/\s+/)[0] || 'text'
      // 已注册语言走语法高亮（hljs 输出自带转义）；未注册回退手工转义，不引入高亮标记
      const body =
        hljs.getLanguage(label) !== undefined
          ? hljs.highlight(text, { language: label, ignoreIllegals: true }).value
          : escapeHtml(text)
      return (
        '<div class="code-block">' +
        '<div class="code-head"><span class="code-lang">' +
        escapeHtml(label) +
        '</span><button type="button" class="code-copy" data-apply>应用到文件</button>' +
        '<button type="button" class="code-copy" data-copy>复制</button></div>' +
        '<pre><code class="hljs language-' +
        escapeHtml(label) +
        '">' +
        body +
        '</code></pre></div>'
      )
    },
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
