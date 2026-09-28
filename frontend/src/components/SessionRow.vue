<script setup lang="ts">
import { useChatStore } from '../stores/chat'
import { relativeTime } from '../composables/relativeTime'
import AppIcon from './AppIcon.vue'

// 会话行：标题（两行：标题 + 相对时间）+ 右侧运行状态（运行中/空闲）+ 行内操作。
// 置顶/会话/空间三个分区共用同一行组件，避免三份拷贝漂移。
const props = defineProps<{
  id: string
  title: string
  lastActiveMs?: number
  pinned?: boolean
  running?: boolean
}>()
const emit = defineEmits<{
  (e: 'select'): void
  (e: 'pin', pinned: boolean): void
  (e: 'rename'): void
  (e: 'remove'): void
}>()

const store = useChatStore()
</script>

<template>
  <div class="group flex items-center gap-1">
    <button
      class="min-w-0 flex-1 rounded-xl px-3 py-2 text-left transition-colors"
      :class="
        props.id === store.sessionId
          ? 'bg-[var(--c-primary-soft)] text-[var(--c-primary)]'
          : 'text-[var(--c-text-dim)] hover:bg-[var(--c-surface-soft)]'
      "
      @click="emit('select')"
    >
      <span class="block truncate text-sm" :class="props.id === store.sessionId ? 'font-medium' : ''">
        {{ props.title || props.id }}
      </span>
      <span
        v-if="props.lastActiveMs"
        class="mt-0.5 block text-[10px] leading-3 text-[var(--c-text-faint)]"
      >
        {{ relativeTime(props.lastActiveMs) }}
      </span>
    </button>
    <!-- 每行右侧：该会话的运行状态（运行中 = 脉冲点 + 文案；空闲 = 静默点） -->
    <span
      v-if="props.running"
      class="flex shrink-0 items-center gap-1 pl-1 text-[10px] font-medium text-[var(--c-primary)]"
      title="该会话正在运行"
    >
      <span class="h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--c-primary)]"></span>
      运行中
    </span>
    <span
      v-else
      class="shrink-0 pl-1 text-[10px] text-[var(--c-text-faint)]"
      title="该会话空闲"
    >
      <span class="inline-block h-1.5 w-1.5 rounded-full bg-[var(--c-border)]"></span>
    </span>
    <button
      class="btn-ghost shrink-0"
      :class="props.pinned ? 'text-[var(--c-primary)] opacity-100' : ''"
      :disabled="store.running"
      :title="props.pinned ? '取消置顶' : '置顶'"
      :aria-label="props.pinned ? '取消置顶' : '置顶'"
      @click="emit('pin', !props.pinned)"
    >
      <AppIcon name="star" :size="13" />
    </button>
    <button
      class="btn-ghost shrink-0"
      :disabled="store.running"
      title="重命名会话"
      aria-label="重命名会话"
      @click="emit('rename')"
    >
      <AppIcon name="pencil" :size="14" />
    </button>
    <button
      class="btn-ghost shrink-0 hover:text-[var(--c-err-text)]"
      :disabled="store.running"
      title="删除会话"
      aria-label="删除会话"
      @click="emit('remove')"
    >
      <AppIcon name="trash" :size="14" />
    </button>
  </div>
</template>
