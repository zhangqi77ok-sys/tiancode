<script setup lang="ts">
import { ref, watch } from 'vue'
import type { ChatMsg } from '../stores/chat'
import AppIcon from './AppIcon.vue'
import MarkdownBody from './MarkdownBody.vue'

const props = defineProps<{ m: ChatMsg }>()

// 用户/助手消息二选一渲染（role 在入库后不再变化）
const isUser = props.m.role === 'user'

// 思考折叠：流式中默认展开，终态自动收起；用户手动开合后以手动为准
const thinkingOpen = ref(!!props.m.streaming)
watch(
  () => props.m.streaming,
  (s, old) => {
    if (old === true && s === false) thinkingOpen.value = false
  },
)

function fmtTime(at?: number): string {
  if (!at) return ''
  return new Date(at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}
</script>

<template>
  <!-- 用户消息：主色实心气泡，右对齐 -->
  <div v-if="isUser" class="flex flex-col items-end gap-1">
    <div class="flex items-center gap-2 text-xs text-[var(--c-text-dim)]">
      <span>YOU</span><span>{{ fmtTime(m.at) }}</span>
    </div>
    <div
      class="max-w-[75%] whitespace-pre-wrap rounded-2xl bg-[var(--c-primary)] px-4 py-2.5 text-sm leading-6 text-white"
    >
      {{ m.content }}
    </div>
  </div>

  <!-- 助手消息：浅紫气泡（与卡片底色区分，修掉旧版层级塌陷）；错误/取消/超时为纯文本 -->
  <div v-else class="flex flex-col items-start gap-1">
    <div class="flex items-center gap-2 text-xs text-[var(--c-text-dim)]">
      <span class="font-medium">AGENT</span><span>{{ fmtTime(m.at) }}</span>
    </div>

    <button
      v-if="m.thinking"
      class="chip text-xs"
      :aria-expanded="thinkingOpen"
      @click="thinkingOpen = !thinkingOpen"
    >
      <AppIcon
        name="chevron-down"
        :size="12"
        class="transition-transform"
        :class="thinkingOpen ? '' : '-rotate-90'"
      />
      思考过程
    </button>
    <pre v-if="m.thinking && thinkingOpen" class="tool-full max-w-[85%] text-[var(--c-text-dim)]">{{ m.thinking }}</pre>

    <div
      class="max-w-[85%] rounded-2xl border px-4 py-3 text-sm leading-6"
      :class="
        m.term === 3 || m.term === 4
          ? 'border-[var(--c-warn)] bg-[var(--c-warn-soft)]'
          : m.error
            ? 'border-[var(--c-err)] bg-[var(--c-err-soft)]'
            : 'border-[var(--c-border)] bg-[var(--c-bubble)]'
      "
    >
      <div v-if="m.error || m.term === 3 || m.term === 4" class="flex items-start gap-2 whitespace-pre-wrap">
        <AppIcon
          name="alert"
          :size="14"
          class="mt-1 shrink-0"
          :class="m.term === 3 || m.term === 4 ? 'text-[var(--c-warn-text)]' : 'text-[var(--c-err-text)]'"
        />
        <span>{{ m.content }}</span>
      </div>
      <template v-else>
        <MarkdownBody :content="m.content" :streaming="m.streaming" />
        <span v-if="m.streaming" class="caret"></span>
      </template>
    </div>
  </div>
</template>
