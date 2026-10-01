// 消息渲染：Markdown → 消毒后的 HTML。
// 为什么必须消毒：渲染的是模型输出（不可信）。原始 HTML 不转义成文本（那会让用户
// 看到 <div> 原文），而是交给末端 DOMPurify 白名单——对齐开源实践（Streamdown/open-webui）。
// GFM + breaks:true 是聊天场景惯例：模型输出的单个换行就是换行，不与后续段落黏连。
import { Marked, type Tokens } from 'marked'
import DOMPurify from 'dompurify'
import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import c from 'highlight.js/lib/languages/c'
import css from 'highlight.js/lib/languages/css'
import diff from 'highlight.js/lib/languages/diff'
import go from 'highlight.js/lib/languages/go'
import java from 'highlight.js/lib/languages/java'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import markdown from 'highlight.js/lib/languages/markdown'
import powershell from 'highlight.js/lib/languages/powershell'
import python from 'highlight.js/lib/languages/python'
import rust from 'highlight.js/lib/languages/rust'
import sql from 'highlight.js/lib/languages/sql'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'

// 按需注册 AI 编程工具最高频的语言（tree-shake 后只打包这些，不引全量 hljs）。
// 语法高亮是 AI 编程工具的立身之本——纯文本代码块可读性不达标（用户 0.2.5 反馈）。
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('c', c)
hljs.registerLanguage('css', css)
hljs.registerLanguage('diff', diff)
hljs.registerLanguage('go', go)
hljs.registerLanguage('java', java)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('json', json)
hljs.registerLanguage('markdown', markdown)
hljs.registerLanguage('powershell', powershell)
hljs.registerLanguage('python', python)
hljs.registerLanguage('rust', rust)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('yaml', yaml)
// 常见别名
hljs.registerAliases(['ts'], { languageName: 'typescript' })
hljs.registerAliases(['js'], { languageName: 'javascript' })
// powershell 有独立语法（变量/参数与 bash 差异大），不再别名成 bash（0.3）；
// sh/shell 仍按 bash 高亮
hljs.registerAliases(['sh', 'shell'], { languageName: 'bash' })
hljs.registerAliases(['yml'], { languageName: 'yaml' })
// vue 单文件组件没有独立 hljs 语法：按 html/xml 着色（结构相同，可读性等价）
hljs.registerAliases(['html', 'vue'], { languageName: 'xml' })

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

// 本次渲染的代码块计数与"从第几块起不写高亮"（阶段 2）。
// 为什么用模块级可变状态：marked 的 renderer 是同步单线程调用，每次 renderMarkdown
// 开头重置即可——不必为了一个选项重建 Marked 实例（重建要重装全部 tokenizer）。
const codeState = { index: 0, plainFrom: Number.POSITIVE_INFINITY }

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
      // 正在输入的那一块（未闭合栅栏）先用纯文本：半截代码逐帧重算高亮既费 CPU，
      // 又让颜色每帧跳；它闭合后整体高亮一次（见 RenderOptions.plainCodeFrom）。
      const plain = codeState.index >= codeState.plainFrom
      codeState.index++
      // 已注册语言走语法高亮（hljs 输出自带转义）；未注册/未闭合回退手工转义，不引入高亮标记
      const body =
        !plain && hljs.getLanguage(label) !== undefined
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

// RenderOptions 是渲染的附加开关。
export interface RenderOptions {
  /**
   * 从第几个（0 基）代码块起不写高亮标记。
   * 流式期间用：未闭合的那个栅栏先用纯文本，等它闭合后（内容已变、缓存键不同）
   * 才整体高亮一次——"只高亮一次"是缓存的自然结果，不需要额外的状态机。
   */
  plainCodeFrom?: number
}

// ---- 渲染结果缓存（阶段 2）----
// 为什么必须有：流式增量会让整张消息列表重渲染，已完成消息会被反复送进来；
// marked + hljs 是纯函数，同样的输入只该算一次（长会话卡顿的主因就是重复解析）。
// 为什么双限（条数 + 总字节）：键与值都是字符串，长会话不能无限长大——把内存吃光
// 比卡顿更难查。命中即刷新插入序，淘汰最旧的一条（LRU）。
const CACHE_MAX_ENTRIES = 400
const CACHE_MAX_BYTES = 8 * 1024 * 1024
const renderCache = new Map<string, string>()
let renderCacheBytes = 0

function cacheKey(src: string, plainCodeFrom: number): string {
  // plainCodeFrom 参与键：同一段文本"未闭合时"与"闭合后"是两份不同结果，不能互相顶掉
  return `${plainCodeFrom}\u0000${src}`
}

function rememberRendered(key: string, html: string): void {
  renderCache.set(key, html)
  renderCacheBytes += key.length + html.length
  while (renderCache.size > CACHE_MAX_ENTRIES || renderCacheBytes > CACHE_MAX_BYTES) {
    const oldest = renderCache.keys().next()
    if (oldest.done) break
    const value = renderCache.get(oldest.value) ?? ''
    renderCache.delete(oldest.value)
    renderCacheBytes -= oldest.value.length + value.length
  }
}

// renderCacheStats / clearRenderCache 供测试断言"同一内容只解析一次"。
export function renderCacheStats(): { entries: number; bytes: number } {
  return { entries: renderCache.size, bytes: renderCacheBytes }
}

export function clearRenderCache(): void {
  renderCache.clear()
  renderCacheBytes = 0
}

export function renderMarkdown(src: string, opts: RenderOptions = {}): string {
  if (!src) return ''
  const plainCodeFrom = opts.plainCodeFrom ?? Number.POSITIVE_INFINITY
  const key = cacheKey(src, plainCodeFrom)
  const hit = renderCache.get(key)
  if (hit !== undefined) {
    renderCache.delete(key) // 命中刷新插入序（LRU）
    renderCache.set(key, hit)
    return hit
  }
  const html = renderUncached(src, plainCodeFrom)
  rememberRendered(key, html)
  return html
}

function renderUncached(src: string, plainCodeFrom: number): string {
  try {
    codeState.index = 0
    codeState.plainFrom = plainCodeFrom
    const html = md.parse(src, { async: false, gfm: true, breaks: true }) as string
    return DOMPurify.sanitize(html, {
      USE_PROFILES: { html: true },
      FORBID_TAGS: ['style', 'iframe', 'form'],
    })
  } catch {
    return `<pre>${escapeHtml(src)}</pre>`
  } finally {
    codeState.index = 0
    codeState.plainFrom = Number.POSITIVE_INFINITY
  }
}

// unclosedCodeFrom 返回"还没闭合的那个代码块"在本次渲染里的序号（0 基）；
// 没有未闭合栅栏时返回 undefined（= 全部照常高亮）。
// 数行首栅栏就够：奇数个说明最后一个没闭合，它前面有 floor(opens/2) 个已闭合的块。
export function unclosedCodeFrom(src: string): number | undefined {
  const opens = src.match(/^[ \t]{0,3}```/gm)?.length ?? 0
  return opens % 2 === 1 ? Math.floor(opens / 2) : undefined
}

// 流式期间专用：把未闭合的 ``` 栅栏补上闭合行——半截代码块不能吞掉后续正文
// （Streamdown「unterminated block」同类处理；整段完成后用原文本渲染，所见即最终结果）。
export function closeUnbalancedFences(src: string): string {
  const opens = src.match(/^[ \t]{0,3}```/gm)?.length ?? 0
  return opens % 2 === 1 ? src + '\n```' : src
}
