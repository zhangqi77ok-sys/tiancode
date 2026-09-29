<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ChatMsg } from '../stores/chat'
import { useChatStore } from '../stores/chat'
import { useToast } from '../composables/useToast'
import AppIcon from './AppIcon.vue'
import ApprovalCard from './ApprovalCard.vue'
import AskCard from './AskCard.vue'
import MarkdownBody from './MarkdownBody.vue'
import ToolCard from './ToolCard.vue'

// 两种形态：
// - 用户消息（role=user）：单气泡，右对齐；
// - agent 回合（run）：单一消息头 + 内部段落流（思考 → 文本 → 工具卡 → 思考 → …）——
//   整个回合是"一个输出"，不再每个助手段各起一个 AGENT 头（0.2.16 用户反馈）。
const props = defineProps<{ m: ChatMsg; run?: ChatMsg[] }>()

const segments = computed<ChatMsg[]>(() => props.run ?? [props.m])
const isUser = computed(() => props.m.role === 'user')

const { push: toast } = useToast()
const store = useChatStore()

const showRetry = computed(() => {
  if (isUser.value || store.running) return false
  const last = store.messages.at(-1)
  if (!last || !segments.value.includes(last)) return false
  return segments.value.some((s) => s.error)
})

async function retry() {
  const lastUser = [...store.messages].reverse().find((m) => m.role === 'user' && m.content.trim())
  if (!lastUser || store.running) return
  await store.send(lastUser.content)
}

// 思考折叠：按段独立记忆；流式段默认展开，终态后回到折叠（用户手动开合后以手动为准）
const thinkingOpen = ref<Record<string, boolean>>({})
function segKey(seg: ChatMsg, i: number): string {
  return seg.id ?? `seg-${i}`
}
function isThinkingOpen(seg: ChatMsg, i: number): boolean {
  return thinkingOpen.value[segKey(seg, i)] ?? !!seg.streaming
}
function toggleThinking(seg: ChatMsg, i: number) {
  thinkingOpen.value[segKey(seg, i)] = !isThinkingOpen(seg, i)
}

// 回合头：时间取首个助手段；耗时取各段之和（通常只有终段带）
const startedAt = computed(() => segments.value.find((s) => s.role === 'assistant')?.at)
const durationMs = computed(() => segments.value.reduce((a, s) => a + (s.durationMs ?? 0), 0))

async function copyMessage() {
  const text = segments.value
    .filter((s) => s.role === 'assistant')
    .map((s) => s.content)
    .filter(Boolean)
    .join('\n\n')
  try {
    await navigator.clipboard.writeText(text)
    toast('info', '已复制回复')
  } catch {
    toast('error', '复制失败：剪贴板不可用')
  }
}

function fmtTime(at?: number): string {
  if (!at) return ''
  return new Date(at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}
</script>

<template>
  <!-- 用户消息：主色实心气泡，右对齐 -->
  <div v-if="isUser" class="flex flex-col items-end gap-1">
    <div class="flex items-center gap-2 text-xs text-[var(--c-text-dim)]">
      <span>你</span><span>{{ fmtTime(m.at) }}</span>
    </div>
    <div
      class="max-w-[75%] whitespace-pre-wrap rounded-2xl bg-[var(--c-primary)] px-4 py-2.5 text-sm leading-6 text-white"
    >
      {{ m.content }}
    </div>
  </div>

  <!-- agent 回合：单一消息头 + 段落流，整体是一个输出 -->
  <div v-else class="flex flex-col items-start gap-1">
    <div class="flex items-center gap-2 text-xs text-[var(--c-text-dim)]">
      <span class="font-medium">tiancode</span><span>{{ fmtTime(startedAt) }}</span>
      <span v-if="durationMs" class="text-[var(--c-text-faint)]">· {{ (durationMs / 1000).toFixed(1) }}s</span>
      <button
        class="ml-1 inline-flex h-5 w-5 items-center justify-center rounded text-[var(--c-text-faint)] opacity-60 transition-opacity hover:text-[var(--c-primary)] hover:opacity-100"
        title="复制回复"
        aria-label="复制回复"
        @click="copyMessage"
      >
        <AppIcon name="copy" :size="12" />
      </button>
    </div>

    <!-- 段落流：ReAct 顺序原样保留，不做二次归并 -->
    <template v-for="(seg, i) in segments" :key="seg.id ?? 'seg-' + i">
      <template v-if="seg.role === 'assistant'">
        <button
          v-if="seg.thinking"
          class="chip text-xs"
          :aria-expanded="isThinkingOpen(seg, i)"
          @click="toggleThinking(seg, i)"
        >
          <AppIcon
            name="chevron-down"
            :size="12"
            class="transition-transform"
            :class="isThinkingOpen(seg, i) ? '' : '-rotate-90'"
          />
          深度思考
        </button>
        <pre
          v-if="seg.thinking && isThinkingOpen(seg, i)"
          class="tool-full max-w-[85%] text-[var(--c-text-dim)]"
          >{{ seg.thinking }}</pre
        >

        <!-- "正在思考"占位（0.2.28）：首块到达前的可见反馈——慢上游/挂起时
             用户看到的是"在等模型"，而不是"什么都没发生" -->
        <div
          v-if="seg.streaming && !seg.content && !seg.thinking"
          class="flex max-w-[85%] items-center gap-1.5 rounded-2xl border border-[var(--c-border)] bg-[var(--c-bubble)] px-4 py-3"
          aria-label="正在思考"
        >
          <span class="h-1.5 w-1.5 animate-bounce rounded-full bg-[var(--c-text-faint)]"></span>
          <span class="h-1.5 w-1.5 animate-bounce rounded-full bg-[var(--c-text-faint)] [animation-delay:150ms]"></span>
          <span class="h-1.5 w-1.5 animate-bounce rounded-full bg-[var(--c-text-faint)] [animation-delay:300ms]"></span>
          <span class="ml-1 text-xs text-[var(--c-text-faint)]">正在思考…</span>
        </div>

        <!-- 纯思考段（无正文）不出空气泡 -->
        <div
          v-if="seg.content || seg.error || seg.term === 3 || seg.term === 4"
          class="max-w-[85%] rounded-2xl border px-4 py-3 text-sm leading-6"
          :class="
            seg.term === 3 || seg.term === 4
              ? 'border-[var(--c-warn)] bg-[var(--c-warn-soft)]'
              : seg.error
                ? 'border-[var(--c-err)] bg-[var(--c-err-soft)]'
                : 'border-[var(--c-border)] bg-[var(--c-bubble)]'
          "
        >
          <div v-if="seg.error || seg.term === 3 || seg.term === 4" class="flex items-start gap-2 whitespace-pre-wrap">
            <AppIcon
              name="alert"
              :size="14"
              class="mt-1 shrink-0"
              :class="seg.term === 3 || seg.term === 4 ? 'text-[var(--c-warn-text)]' : 'text-[var(--c-err-text)]'"
            />
            <span>{{ seg.content }}</span>
          </div>
          <template v-else>
            <MarkdownBody :content="seg.content" :streaming="seg.streaming" />
          </template>
        </div>
      </template>

      <!-- 空卡守卫（0.0.06）：没有标题/内容/diff 且非执行中的工具事件不出卡——
           绝不让用户看到一张"什么都没有"的空卡 -->
      <ToolCard v-else-if="seg.role === 'tool' && (seg.title || seg.content || seg.diff || seg.status === 'running')" :m="seg" />
      <!-- role === 'todo' 刻意不渲染：任务清单由悬浮件（FloatingTodo）承载，不随对话滚走 -->
      <AskCard v-else-if="seg.role === 'ask'" :m="seg" />
      <ApprovalCard v-else-if="seg.role === 'approval'" :m="seg" />
    </template>
    <button v-if="showRetry" class="chip mt-1 text-xs" @click="retry">重试上一问</button>
  </div>
</template>
