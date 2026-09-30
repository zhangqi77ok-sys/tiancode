<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { closeUnbalancedFences, renderMarkdown } from '../markdown'
import { useDialogs } from '../composables/useDialogs'
import { useToast } from '../composables/useToast'
import { useChatStore } from '../stores/chat'

// 流式期间每个 chunk 都重跑 marked+DOMPurify 会让长回复卡顿：300ms 合帧渲染；
// 且先把未闭合的 ``` 补齐（Streamdown「unterminated block」同款处理）——
// 半截代码栅栏不能吞掉后续正文，否则流式过程中排版会乱跳；
// 终态立即渲染原始全文，保证所见即最终结果。
const RENDER_THROTTLE_MS = 300

const props = defineProps<{ content: string; streaming?: boolean }>()

const html = ref(renderMarkdown(props.content))
let timer: ReturnType<typeof setTimeout> | null = null

const { push: toast } = useToast()
const dialogs = useDialogs()
const store = useChatStore()

// 事件委托：marked 渲染出的复制/应用按钮统一在此处理——
// v-html 内的元素无法直接绑 Vue 事件，委托到容器是标准做法
async function onContentClick(e: MouseEvent) {
  const target = e.target as HTMLElement
  const applyBtn = target.closest('[data-apply]')
  if (applyBtn) {
    const code = applyBtn.closest('.code-block')?.querySelector('pre code')?.textContent ?? ''
    const lang = applyBtn.closest('.code-block')?.querySelector('.code-lang')?.textContent ?? ''
    // 目标文件：语言名可当扩展名时给默认名；用户在对话框里改路径（不猜）
    const ext = lang && /^[a-z0-9]{1,6}$/i.test(lang) ? `.${lang}` : '.txt'
    const path = await dialogs.prompt({
      title: '应用到文件',
      message: '输入目标文件路径（工作区相对，确认后直接写入）：',
      value: `untitled${ext}`,
    })
    if (!path) return // 取消/未选择：不产生任何写盘调用
    try {
      const res = await store.proposeApplyCode(path, code)
      toast('info', `已写入 ${res?.path ?? path}`)
    } catch (err) {
      toast('error', String(err instanceof Error ? err.message : err))
    }
    return
  }
  const btn = target.closest('[data-copy]')
  if (!btn) return
  const code = btn.closest('.code-block')?.querySelector('pre code')?.textContent ?? ''
  try {
    await navigator.clipboard.writeText(code)
    toast('info', '代码已复制')
  } catch {
    toast('error', '复制失败：剪贴板不可用')
  }
}

function renderNow(src: string) {
  html.value = renderMarkdown(props.streaming ? closeUnbalancedFences(src) : src)
}

// 内容增量：流式期间合帧；非流式（历史回放）直接渲染
watch(
  () => props.content,
  (content) => {
    if (!props.streaming) {
      html.value = renderMarkdown(content)
      return
    }
    if (timer) return // 已有待执行帧：本帧合流，避免每 chunk 都排定时器
    timer = setTimeout(() => {
      timer = null
      renderNow(props.content)
    }, RENDER_THROTTLE_MS)
  },
)

// 流结束：清掉挂起帧并立即渲染最终内容（原文，无栅栏补齐）
watch(
  () => props.streaming,
  (streaming) => {
    if (streaming) return
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
    html.value = renderMarkdown(props.content)
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
