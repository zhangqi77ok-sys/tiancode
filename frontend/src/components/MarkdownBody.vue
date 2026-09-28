<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { renderMarkdown } from '../markdown'

// 流式期间每个 chunk 都重跑 marked+DOMPurify 会让长回复卡顿：300ms 合帧渲染；
// 终态（streaming=false）立即渲染最终版，保证所见即所得。
const RENDER_THROTTLE_MS = 300

const props = defineProps<{ content: string; streaming?: boolean }>()

const html = ref(renderMarkdown(props.content))
let timer: ReturnType<typeof setTimeout> | null = null

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
      html.value = renderMarkdown(props.content)
    }, RENDER_THROTTLE_MS)
  },
)

// 流结束：清掉挂起帧并立即渲染最终内容
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
  <!-- renderMarkdown 内部经 DOMPurify 消毒（见 markdown.ts），这里只负责节流 -->
  <div class="markdown-body" v-html="html"></div>
</template>
