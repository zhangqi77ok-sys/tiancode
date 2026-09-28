<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useChatStore } from '../stores/chat'
import { useWorkspaceStore } from '../stores/workspace'
import { useAutoScroll } from '../composables/useAutoScroll'
import { groupMessages } from '../composables/messageGrouping'
import AppIcon from './AppIcon.vue'
import ApprovalCard from './ApprovalCard.vue'
import AskCard from './AskCard.vue'
import MessageBubble from './MessageBubble.vue'
import TodoCard from './TodoCard.vue'
import ToolCard from './ToolCard.vue'

// 建议提示：点击回填输入框（由 App 把草稿传给 Composer）
const emit = defineEmits<{ (e: 'suggest', text: string): void }>()

const store = useChatStore()
const ws = useWorkspaceStore()

// 空态里的"选择工作区"主行动：选择目录 → 切换 → 开新对话（与顶栏/侧栏同语义）
async function pickWorkspace() {
  const ok = await ws.pickAndSet()
  if (ok) await store.newSession()
}

// 建议按"有没有工作区"分两组：无工作区时本地工具未注册（纯对话），
// 给"读文件/跑测试"类提示词等于指引模型撞墙——文案必须与实际能力一致
const suggestions = computed(() =>
  ws.path
    ? ['介绍这个项目', '读取 go.mod 前 5 行并复述 module 名', '运行 go test ./internal/core/tools/']
    : ['写一个 Python 快排并解释思路', '解释常见的正则陷阱', '对比 Redis 与 Memcached 的差异'],
)

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
      <p class="text-sm text-[var(--c-text-dim)]">
        {{ ws.path ? '发一条消息开始，或试试：' : '未选择工作区 · 纯对话模式（文件与命令工具不可用）' }}
      </p>
      <div class="flex flex-wrap justify-center gap-2">
        <button v-for="s in suggestions" :key="s" class="chip" @click="emit('suggest', s)">
          {{ s }}
        </button>
      </div>
      <button v-if="!ws.path" class="chip gap-1.5 border-[var(--c-primary)] text-[var(--c-primary)]" @click="pickWorkspace">
        <AppIcon name="folder" :size="13" /> 选择工作区（解锁文件与命令工具）
      </button>
    </div>

    <template v-for="item in items" :key="item.key">
      <ToolCard v-if="item.kind === 'tool'" :m="item.m" />
      <TodoCard v-else-if="item.kind === 'todo'" :m="item.m" />
      <ApprovalCard v-else-if="item.kind === 'approval'" :m="item.m" />
      <AskCard v-else-if="item.kind === 'ask'" :m="item.m" />
      <MessageBubble v-else-if="item.kind === 'turn'" :m="item.run[0]" :run="item.run" />
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
