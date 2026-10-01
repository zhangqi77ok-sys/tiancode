<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { closeUnbalancedFences, renderMarkdown, unclosedCodeFrom } from '../markdown'
import { openExternal } from '../wails'
import { defaultFileName } from '../composables/codeExt'
import { useDialogs } from '../composables/useDialogs'
import { errText } from '../composables/errText'
import { useClipboard } from '../composables/useClipboard'
import { useToast } from '../composables/useToast'
import { useChatStore } from '../stores/chat'

// 流式期间每个 chunk 都重跑 marked+DOMPurify 会让长回复卡顿：300ms 合帧渲染；
// 且先把未闭合的 ``` 补齐（Streamdown「unterminated block」同款处理）——
// 半截代码栅栏不能吞掉后续正文，否则流式过程中排版会乱跳；
// 终态立即渲染原始全文，保证所见即最终结果。
//
// 阶段 2 两点变化：① 结果走 markdown.ts 的缓存（同一内容只解析一次，
// 让"整表重渲染"不再等于"整表重解析"）；② 未闭合的那个代码块先用纯文本，
// 闭合后再高亮一次——配合缓存，那一次只发生一次。
const RENDER_THROTTLE_MS = 300

const props = defineProps<{ content: string; streaming?: boolean }>()

// 唯一渲染入口：终态用原文全高亮；流式补栅栏 + 让正在输入的那块保持纯文本。
function renderFor(content: string, streaming: boolean): string {
  if (!streaming) return renderMarkdown(content)
  return renderMarkdown(closeUnbalancedFences(content), { plainCodeFrom: unclosedCodeFrom(content) })
}

const html = ref(renderFor(props.content, !!props.streaming))
let timer: ReturnType<typeof setTimeout> | null = null

const { push: toast } = useToast()
const { copy } = useClipboard()
const dialogs = useDialogs()
const store = useChatStore()

// 链接分类处置（此前一律塞给 BrowserNavigate：#锚点/相对链接/mailto 会被后端
// 拒掉再 fallback 进系统浏览器，开出一堆垃圾 URL）：
//   · #fragment → 页内锚点：滚到对应元素（不进任何浏览器）；
//   · http/https → 右侧驾驶舱（与模型同一浏览器，所见即所控）；
//   · 其他安全 scheme（mailto/tel 等）→ 直接交系统浏览器，不走驾驶舱绕路；
//   · 无 scheme 的相对链接 → 就地忽略：前端没有可靠的解析基准（工作区根≠文档基址），
//     开到哪都是错的，宁可不动也不开垃圾 URL。
// scheme 白名单取 DOMPurify 默认放行的安全集合，杜绝 javascript: 之类注入面。
const SAFE_EXTERNAL_SCHEMES = /^(mailto|tel|callto|sms|xmpp):/i

function handleLinkClick(anchor: HTMLAnchorElement) {
  const href = anchor.getAttribute('href') ?? ''
  if (href.startsWith('#')) {
    // 页内锚点：jsdom/真实 WebView 都可能没有目标元素（渲染器不产 heading id），
    // 找不到就静默不动——绝不能让它落到系统浏览器
    const id = decodeURIComponent(href.slice(1))
    document.getElementById(id)?.scrollIntoView()
    return
  }
  if (/^https?:\/\//i.test(href)) {
    void store.openLinkInBrowser(href)
    return
  }
  if (SAFE_EXTERNAL_SCHEMES.test(href)) {
    openExternal(href)
    return
  }
  // 相对链接 / 未知 scheme：忽略（不导航、不开浏览器）
}

// 事件委托：marked 渲染出的复制/应用按钮统一在此处理——
// v-html 内的元素无法直接绑 Vue 事件，委托到容器是标准做法
async function onContentClick(e: MouseEvent) {
  const target = e.target as HTMLElement
  // 链接必须最先拦（bug 实证：放任默认行为会把整个应用窗口导航走——WebView
  // 没有地址栏和后退按钮，用户被困在目标页面里只能重启）。
  const anchor = target.closest<HTMLAnchorElement>('a[href]')
  if (anchor) {
    e.preventDefault()
    handleLinkClick(anchor)
    return
  }
  const applyBtn = target.closest('[data-apply]')
  if (applyBtn) {
    const code = applyBtn.closest('.code-block')?.querySelector('pre code')?.textContent ?? ''
    const lang = applyBtn.closest('.code-block')?.querySelector('.code-lang')?.textContent ?? ''
    // 目标文件：默认名用「语言名 → 扩展名」的小映射（阶段 3-3：语言名当扩展名会写出
    // untitled.typescript / untitled.bash），映射不到用 .txt；用户可在对话框里改路径
    const path = await dialogs.prompt({
      title: '应用到文件',
      message: '输入目标文件路径（工作区相对，确认后直接写入）：',
      value: defaultFileName(lang),
    })
    if (!path) return // 取消/未选择：不产生任何写盘调用
    try {
      const res = await store.proposeApplyCode(path, code)
      toast('info', `已写入 ${res?.path ?? path}`)
    } catch (err) {
      toast('error', errText(err))
    }
    return
  }
  const btn = target.closest('[data-copy]')
  if (!btn) return
  const code = btn.closest('.code-block')?.querySelector('pre code')?.textContent ?? ''
  await copy(code, { success: '代码已复制' })
}

function renderNow(src: string) {
  html.value = renderFor(src, true)
}

// 内容增量：流式期间合帧；非流式（历史回放）直接渲染
watch(
  () => props.content,
  (content) => {
    if (!props.streaming) {
      html.value = renderFor(content, false)
      return
    }
    if (timer) return // 已有待执行帧：本帧合流，避免每 chunk 都排定时器
    timer = setTimeout(() => {
      timer = null
      renderNow(props.content)
    }, RENDER_THROTTLE_MS)
  },
)

// 流结束：清掉挂起帧并立即渲染最终内容（原文，无栅栏补齐，全部高亮）
watch(
  () => props.streaming,
  (streaming) => {
    if (streaming) return
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
    html.value = renderFor(props.content, false)
  },
)

onBeforeUnmount(() => {
  if (timer) clearTimeout(timer)
})
</script>

<template>
  <!-- renderMarkdown 内部经 DOMPurify 消毒（见 markdown.ts），这里只负责流式节流与栅栏补齐；
       streaming 时容器带 md-caret 类：光标由 CSS ::after 内联在末尾，不再单独占行 -->
  <div class="markdown-body" :class="{ 'md-caret': streaming }" v-html="html" @click="onContentClick"></div>
</template>
