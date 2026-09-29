<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useChannelStore } from '../stores/channels'
import { useChatStore } from '../stores/chat'
import { useWorkspaceStore } from '../stores/workspace'
import { useAutoScroll } from '../composables/useAutoScroll'
import { groupMessages } from '../composables/messageGrouping'
import AppIcon from './AppIcon.vue'
import ApprovalCard from './ApprovalCard.vue'
import AskCard from './AskCard.vue'
import MessageBubble from './MessageBubble.vue'
import ToolCard from './ToolCard.vue'

// 建议提示：点击回填输入框（由 App 把草稿传给 Composer）
const emit = defineEmits<{ (e: 'suggest', text: string): void }>()

const store = useChatStore()
const channels = useChannelStore()
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
    ? ['介绍这个项目', '这个目录里最值得先看的是什么', '指出这里最明显的一处风险']
    : ['写一个快排并解释思路', '帮我看这段报错可能是什么原因', '把下面的需求拆成可执行的步骤'],
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

// 打开/切换会话与历史载入完成：**强制**落到最新（force 无视锚定）。
// 为什么必须：冷启动 init 选中历史会话时，Replay 赋值触发的 toBottom 打在
// "正在载入历史…"的矮容器上（无效），历史渲染完成后容器变长会把锚定态冲掉，
// 视图停在顶部——用户在长历史底部发送的消息看不到，以为"没显示"（0.2.30 实机：
// "打开软件就直接对话没显示，切换会话才正常"）。打开会话落在最新处是通用惯例。
watch(
  () => [store.sessionId, store.loadingSession] as const,
  async ([, loading]) => {
    if (loading) return
    await toBottom(true)
  },
)

// 用户主动发送（缓冲尾部出现新的 user 消息）：强制落到底——发送是主动动作，
// 用户期望立刻看到自己的消息（与"流式期间保持锚定"纪律不冲突：force 仅此一处）。
const lastUserId = computed(() => {
  for (let i = store.messages.length - 1; i >= 0; i--) {
    const m = store.messages[i]
    if (m.role === 'user') return m.id ?? ''
  }
  return ''
})
watch(lastUserId, (id) => {
  if (id) void toBottom(true)
})
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
  <div class="relative flex min-h-0 flex-1 flex-col">
  <div
    ref="scroller"
    class="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4"
    role="log"
    aria-label="对话记录"
    @scroll.passive="onScroll"
  >
    <!-- 历史载入中：先于空态渲染（否则点开有历史的会话会闪一下"没有消息"） -->
    <div v-if="store.loadingSession" class="flex h-full items-center justify-center">
      <p class="text-sm text-[var(--c-text-dim)]">正在载入历史…</p>
    </div>

    <!-- 空状态 + 建议 chips -->
    <div v-else-if="!store.messages.length" class="flex h-full flex-col items-center justify-center gap-4">
      <p class="text-sm text-[var(--c-text-dim)]">
        {{
          ws.path
            ? `当前模型 ${channels.activeModel || '未配置'} · 发一条消息开始，或试试：`
            : `未选择工作区 · 纯对话（文件与命令不可用）· 当前模型 ${channels.activeModel || '未配置'}`
        }}
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
      <!-- 空卡守卫（0.0.06）：无标题/内容/diff 且非执行中的工具事件不出卡 -->
      <ToolCard
        v-if="item.kind === 'tool' && (item.m.title || item.m.content || item.m.diff || item.m.status === 'running')"
        :m="item.m"
      />
      <!-- kind === 'todo' 刻意不渲染：任务清单改由悬浮件（FloatingTodo 挂在对话面板上）
           呈现，不再随消息流滚走。数据仍在 store/账本里（重放与实时同源），只是换了载体 -->
      <ApprovalCard v-else-if="item.kind === 'approval'" :m="item.m" />
      <AskCard v-else-if="item.kind === 'ask'" :m="item.m" />
      <MessageBubble v-else-if="item.kind === 'turn'" :m="item.run[0]" :run="item.run" />
      <MessageBubble v-else-if="item.kind === 'single'" :m="item.m" />
    </template>
  </div>

  <!-- 锚定丢失时的"回到底部"：位置在消息区内、不压输入框 -->
  <button
    v-if="!anchored && store.messages.length"
    class="chip absolute bottom-3 right-5 z-10 shadow-[var(--shadow-float)]"
    @click="toBottom(true)"
  >
    回到底部
  </button>
  </div>
</template>
