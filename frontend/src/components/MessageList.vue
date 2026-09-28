<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useChatStore } from '../stores/chat'
import { useAutoScroll } from '../composables/useAutoScroll'
import { groupMessages } from '../composables/messageGrouping'
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

// 渲染分组：纯函数（composables/messageGrouping.ts，有单测）——
// 工具卡并入助手回合块（思考 → 执行 → 回复），绝不冒充消息气泡、绝不双重渲染
const items = computed(() => groupMessages(store.messages))
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

    <template v-for="item in items" :key="item.key">
      <ToolCard v-if="item.kind === 'tool'" :m="item.m" />
      <MessageBubble v-else-if="item.kind === 'turn'" :m="item.m" :tools="item.tools" />
      <MessageBubble v-else :m="item.m" />
    </template>
  </div>

  <!-- 锚定丢失时的"回到底部"：位置在消息区内、不压输入框 -->
  <button
    v-if="!anchored && store.messages.length"
    class="chip fixed bottom-28 right-8 z-10 shadow-[var(--shadow-float)]"
    @click="toBottom(true)"
  >
    回到底部
  </button>
</template>
