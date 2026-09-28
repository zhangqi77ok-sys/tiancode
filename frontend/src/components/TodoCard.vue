<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ChatMsg } from '../stores/chat'
import AppIcon from './AppIcon.vue'

// 任务清单卡（对齐商用编码智能体）：任务清单 N/M 已完成 + 可折叠勾选行。
// 数据来自 todo 工具的全量快照（内核 TodoEvent / 账本 EventTodo），单卡原地更新。
const props = defineProps<{ m: ChatMsg }>()

const open = ref(true)
const done = computed(() => (props.m.todos ?? []).filter((t) => t.status === 'done').length)
</script>

<template>
  <div
    class="w-full max-w-[94%] overflow-hidden rounded-xl border border-[var(--c-border)] bg-[var(--c-surface-soft)]"
  >
    <button
      class="flex w-full items-center gap-2 px-3 py-2 text-xs"
      :aria-expanded="open"
      @click="open = !open"
    >
      <AppIcon name="check" :size="13" class="shrink-0 text-[var(--c-primary)]" />
      <span class="font-medium text-[var(--c-text)]">任务清单</span>
      <span class="text-[var(--c-text-faint)]">{{ done }}/{{ m.todos?.length ?? 0 }} 已完成</span>
      <AppIcon
        name="chevron-down"
        :size="12"
        class="ml-auto shrink-0 text-[var(--c-text-faint)] transition-transform"
        :class="open ? '' : '-rotate-90'"
      />
    </button>
    <div v-if="open" class="space-y-1 px-3 pb-2.5">
      <div v-for="(t, i) in m.todos" :key="i" class="flex items-start gap-2 text-xs leading-5">
        <span
          class="mt-0.5 flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-full border"
          :class="
            t.status === 'done'
              ? 'border-[var(--c-ok)] bg-[var(--c-ok)] text-white'
              : t.status === 'in_progress'
                ? 'border-[var(--c-primary)]'
                : 'border-[var(--c-border)]'
          "
        >
          <AppIcon v-if="t.status === 'done'" name="check" :size="9" />
        </span>
        <span
          :class="
            t.status === 'done'
              ? 'text-[var(--c-text-faint)] line-through'
              : t.status === 'in_progress'
                ? 'font-medium text-[var(--c-text)]'
                : 'text-[var(--c-text-dim)]'
          "
        >
          {{ t.text }}
        </span>
      </div>
    </div>
  </div>
</template>
