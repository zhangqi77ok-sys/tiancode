<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useChatStore } from '../stores/chat'
import { useAutoScroll } from '../composables/useAutoScroll'
import ApprovalCard from './ApprovalCard.vue'
import MessageBubble from './MessageBubble.vue'
import ToolCard from './ToolCard.vue'

// 建议提示：点击回填输入框（由 App 把草稿传给 Composer）
const emit = defineEmits<{ (e: 'suggest', text: string): void }>()

const store = useChatStore()

const suggestions = [
  '介绍这个项目',
  '读取 go.mod 前 5 行并复述 module 名',
  '运行 go test ./internal/core/tools/',
]

const scroller = ref<HTMLElement | null>(null)
const { anchored, onScroll, toBottom } = useAutoScroll(scroller)

// 最后一条消息：流式增量只改尾部消息，盯着它即可，避免深度监听整表
const lastMsg = computed(() => store.messages[store.messages.length - 1])

// 新消息与流式增量都只在"用户锚定底部"时跟随滚动（修掉旧版滚动劫持）
watch(
  () => store.messages.length,
  () => {
    void toBottom()
  },
)
watch(
  () => lastMsg.value?.content.length ?? 0,
  () => {
    void toBottom()
  },
)
watch(
  () => lastMsg.value?.thinking?.length ?? 0,
  () => {
    void toBottom()
  },
)

// 稳定 key：优先消息 id（入库统一发号）；缺 id 仅出现在测试直插场景，回退下标
function keyOf(i: number, id?: string): string {
  return id ?? `i-${i}`
}
</script>

<template>
  <div
    ref="scroller"
    class="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4"
    role="log"
    aria-label="对话记录"
    @scroll.passive="onScroll"
  >
    <!-- 空状态 + 建议 chips -->
    <div v-if="!store.messages.length" class="flex h-full flex-col items-center justify-center gap-4">
      <p class="text-sm text-[var(--c-text-dim)]">发一条消息开始，或试试：</p>
      <div class="flex flex-wrap justify-center gap-2">
        <button v-for="s in suggestions" :key="s" class="chip" @click="emit('suggest', s)">
          {{ s }}
        </button>
      </div>
    </div>

    <template v-for="(m, i) in store.messages" :key="keyOf(i, m.id)">
      <ToolCard v-if="m.role === 'tool'" :m="m" />
      <ApprovalCard v-else-if="m.role === 'approval'" :m="m" />
      <MessageBubble v-else :m="m" />
    </template>
  </div>

  <!-- 锚定丢失时的"回到底部"：长会话回看后不必手动滚回 -->
  <button
    v-if="!anchored && store.messages.length"
    class="chip fixed bottom-24 right-6 z-10 shadow-[var(--shadow-float)]"
    @click="toBottom(true)"
  >
    回到底部
  </button>
</template>
