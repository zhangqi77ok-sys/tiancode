<script setup lang="ts">
import { ref } from 'vue'
import type { ChatMsg } from '../stores/chat'
import AppIcon from './AppIcon.vue'

// diff 行着色：只按前缀判定（diff 由内核生成，格式稳定）；
// + / - 行作文字用 -text 色（AA 达标），装饰色只给圆点
function diffLineClass(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'text-[var(--c-text-dim)]'
  if (line.startsWith('@@')) return 'text-[var(--c-primary)]'
  if (line.startsWith('+')) return 'text-[var(--c-ok-text)]'
  if (line.startsWith('-')) return 'text-[var(--c-err-text)]'
  return 'text-[var(--c-text-dim)]'
}

// 摘要截断：药丸内只示意，全文在展开区
function chipText(s: string): string {
  return s.length > 200 ? s.slice(0, 200) + '…' : s
}

defineProps<{ m: ChatMsg }>()

// 折叠态是组件本地状态：key 稳定（消息 id）后，流式更新/新工具卡插入都不再误伤它
const open = ref(false)
</script>

<template>
  <div class="flex max-w-full flex-col items-start gap-1.5">
    <button
      class="inline-flex max-w-full items-center gap-2 rounded-full border px-3 py-1.5 text-xs"
      :class="
        m.status === 'error'
          ? 'border-[var(--c-err)] bg-[var(--c-err-soft)] text-[var(--c-err-text)]'
          : 'border-[var(--c-border)] bg-[var(--c-ok-soft)] text-[var(--c-text-dim)]'
      "
      :aria-expanded="open"
      @click="open = !open"
    >
      <span
        class="h-1.5 w-1.5 shrink-0 rounded-full"
        :class="m.status === 'error' ? 'bg-[var(--c-err)]' : 'bg-[var(--c-ok)]'"
      ></span>
      <span class="shrink-0 font-medium text-[var(--c-text)]">{{ m.toolName }}</span>
      <span class="min-w-0 truncate">{{ chipText(m.content) }}</span>
      <AppIcon
        name="chevron-down"
        :size="12"
        class="shrink-0 transition-transform"
        :class="open ? '' : '-rotate-90'"
      />
    </button>

    <pre v-if="open" class="tool-full max-w-[85%]">{{ m.content }}</pre>

    <!-- 编辑类工具附结构化 diff（内核字段透传，UI 不做文本解析） -->
    <details
      v-if="m.diff"
      class="w-full max-w-[90%] rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)]"
    >
      <summary class="cursor-pointer px-3 py-1.5 text-xs text-[var(--c-text-dim)]">变更预览</summary>
      <div class="max-h-72 overflow-auto whitespace-pre px-3 pb-2 font-mono text-xs leading-5">
        <div v-for="(l, li) in m.diff.split('\n')" :key="li" :class="diffLineClass(l)">{{ l }}</div>
      </div>
    </details>
  </div>
</template>
