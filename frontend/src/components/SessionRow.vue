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
  /** 等待用户答复的请求数（未决审批 + 未答问答）——后台会话挂起时的可见提示 */
  pending?: number
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
    <span
      v-if="props.pending"
      class="stat shrink-0 border-[var(--c-warn)] px-1.5 py-0.5 text-[10px] text-[var(--c-warn-text)]"
      title="有等待你答复的请求（审批/问答）——点击该会话处理"
    >
      待确认
    </span>
    <span
      v-if="props.running"
      class="flex shrink-0 items-center gap-1 pl-1 text-[10px] font-medium text-[var(--c-primary)]"
      title="该会话正在运行"
    >
      <span class="h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--c-primary)]"></span>
      运行中
    </span>
    <button
      v-if="props.pinned"
      class="btn-ghost shrink-0 text-[var(--c-primary)] opacity-100 disabled:cursor-not-allowed disabled:opacity-40"
      :disabled="props.running"
      title="取消置顶"
      aria-label="取消置顶"
      @click="emit('pin', false)"
    >
      <AppIcon name="star" :size="13" />
    </button>
    <!-- 操作默认让出标题宽度；悬停或键盘焦点进入该行时再出现 -->
    <div class="flex shrink-0 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100">
      <button
        v-if="!props.pinned"
        class="btn-ghost shrink-0 disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="props.running"
        title="置顶"
        aria-label="置顶"
        @click="emit('pin', true)"
      >
        <AppIcon name="star" :size="13" />
      </button>
      <button
        class="btn-ghost shrink-0 disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="props.running"
        title="重命名会话"
        aria-label="重命名会话"
        @click="emit('rename')"
      >
        <AppIcon name="pencil" :size="14" />
      </button>
      <button
        class="btn-ghost shrink-0 hover:text-[var(--c-err-text)] disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="props.running"
        title="删除会话"
        aria-label="删除会话"
        @click="emit('remove')"
      >
        <AppIcon name="trash" :size="14" />
      </button>
    </div>
  </div>
</template>
