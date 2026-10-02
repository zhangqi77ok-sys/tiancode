<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useChannelStore } from '../stores/channels'
import { useChatStore, type PendingAttachment } from '../stores/chat'
import { useWorkspaceStore } from '../stores/workspace'
import { useAutoScroll } from '../composables/useAutoScroll'
import { groupMessages, stabilizeItems, type RenderItem } from '../composables/messageGrouping'
import { growFrom, initialFrom, shouldGrow, trimFrom } from '../composables/messageWindow'
import { registerEsc } from '../composables/useEsc'
import { shortDir } from '../composables/workspaceLabel'
import ApprovalCard from './ApprovalCard.vue'
import AskCard from './AskCard.vue'
import MessageBubble from './MessageBubble.vue'
import ToolCard from './ToolCard.vue'
import AppIcon from './AppIcon.vue'

// 建议提示：点击回填输入框（由 App 把草稿传给 Composer）；
// 重跑（第 7 批）：用户气泡点「重跑」→ 原文与附件交回输入框（撤回与分叉在发送时确认）
const emit = defineEmits<{
  (e: 'suggest', text: string): void
  (e: 'rerun', payload: { seq: number; text: string; attachments: PendingAttachment[] }): void
}>()

const store = useChatStore()
const channels = useChannelStore()
const ws = useWorkspaceStore()

// 建议按"有没有工作区"分两组：无工作区时本地工具未注册（纯对话），
// 给"读文件/跑测试"类提示词等于指引模型撞墙——文案必须与实际能力一致
const suggestions = computed(() =>
  ws.path
    ? ['介绍这个项目', '这个目录里最值得先看的是什么', '指出这里最明显的一处风险']
    : ['写一个快排并解释思路', '帮我看这段报错可能是什么原因', '把下面的需求拆成可执行的步骤'],
)

const scroller = ref<HTMLElement | null>(null)
const { anchored, onScroll, toBottom } = useAutoScroll(scroller)

// ---- 窗口（阶段 2）：长会话只挂视口附近的回合 ----
// allItems 是完整分组（顺序与键完全不变），items 只是它的一段切片。
// 尾部永远挂着（贴底跟随 / 发送后到底 / 切换会话落最新都指着尾部），
// 老的一侧按块增挂、超上限再按块裁——规则是纯函数，见 composables/messageWindow.ts。
let prevItems: RenderItem[] = []
const allItems = computed(() => {
  prevItems = stabilizeItems(prevItems, groupMessages(store.messages))
  return prevItems
})
const fromIndex = ref(0)
const items = computed(() => allItems.value.slice(fromIndex.value))

// 滚动：先走既有锚定判定，再维护窗口。
// 向上补块后要做等高补偿（新内容在视口上方，不补会让视图整体下滑）。
async function onScrollWindow() {
  onScroll()
  const node = scroller.value
  if (!node) return
  if (shouldGrow(fromIndex.value, node.scrollTop)) {
    const before = node.scrollHeight
    fromIndex.value = growFrom(fromIndex.value, allItems.value.length)
    await nextTick()
    node.scrollTop += node.scrollHeight - before
    return
  }
  // 已挂载到窗口起点而投影还有更早的历史：向上补一页（0.3 尾屏优先加载）。
  // 等高补偿与 grow 同理——更早的消息插在视口上方。
  if (fromIndex.value === 0 && store.olderAvailable && !store.loadingOlder) {
    const before = node.scrollHeight
    await store.loadOlder()
    await nextTick()
    if (store.messages.length) node.scrollTop += node.scrollHeight - before
    return
  }
  await trimIfNeeded()
}

// 只在贴底时裁掉最老的一块：用户在上方看历史时裁顶部＝把他正在看的内容抽走。
async function trimIfNeeded() {
  const next = trimFrom(fromIndex.value, allItems.value.length, anchored.value)
  if (next === fromIndex.value) return
  fromIndex.value = next
  await nextTick()
  void toBottom(true)
}

// 最后一条消息：流式增量只改尾部消息，盯着它即可，避免深度监听整表
const lastMsg = computed(() => store.messages[store.messages.length - 1])

// 新消息与流式增量都只在"用户锚定底部"时跟随滚动（修掉旧版滚动劫持）
watch(
  () => store.messages.length,
  async () => {
    await toBottom()
    await trimIfNeeded() // 尾部在长，若已超上限且贴底就顺手裁最老一块
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
    fromIndex.value = initialFrom(allItems.value.length) // 窗口复位：只挂尾部
    await toBottom(true)
  },
  { immediate: true },
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

// ---- 会话内搜索（Ctrl+F，0.0.19）----
// 在当前缓冲的消息正文里找（用户/助手正文与问答卡；工具卡正文太碎不搜）。
// 命中项可跳转：目标不在窗口内时先扩窗（fromIndex 前移）再滚动 + 高亮闪烁。
const searchOpen = ref(false)
const query = ref('')
const searchInput = ref<HTMLInputElement | null>(null)
const hitIndex = ref(0)
let offEsc: (() => void) | null = null

function itemText(it: RenderItem): string {
  if (it.kind === 'turn') return it.run.map((m) => m.content).join('\n')
  if (it.kind === 'single' || it.kind === 'tool' || it.kind === 'approval' || it.kind === 'ask') {
    return [it.m.content, it.m.question].filter(Boolean).join('\n')
  }
  return ''
}

const hits = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return [] as RenderItem[]
  return allItems.value.filter((it) => {
    const t = itemText(it)
    return t.length > 0 && t.toLowerCase().includes(q)
  })
})

function openSearch() {
  searchOpen.value = true
  hitIndex.value = 0
  void nextTick(() => searchInput.value?.focus())
}
function closeSearch() {
  searchOpen.value = false
  query.value = ''
}
function moveHit(delta: number) {
  const n = hits.value.length
  if (!n) return
  hitIndex.value = (hitIndex.value + delta + n) % n
  void jumpToHit(hits.value[hitIndex.value])
}

async function jumpToHit(it: RenderItem | undefined) {
  if (!it || !scroller.value) return
  const absIdx = allItems.value.indexOf(it)
  if (absIdx < 0) return
  // 目标在窗口外（上方被裁）：前移窗口起点再渲染
  if (absIdx < fromIndex.value) fromIndex.value = Math.max(0, absIdx - 5)
  await nextTick()
  const el = scroller.value.querySelector<HTMLElement>(`[data-skey="${cssEscape(it.key)}"]`)
  if (!el) return
  el.scrollIntoView({ block: 'center' })
  el.classList.remove('search-flash')
  // 强制重排让动画可重复触发
  void el.offsetWidth
  el.classList.add('search-flash')
}

function cssEscape(v: string): string {
  return v.replace(/[^a-zA-Z0-9_-]/g, (c) => `\${c}`)
}

function onListKeydown(e: KeyboardEvent) {
  // 模态打开时不下手（渠道管理等面板盖在上面，搜索条是背后的事）
  if (document.querySelector('[role="dialog"]')) return
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'f') {
    e.preventDefault()
    openSearch()
  }
}
onMounted(() => {
  window.addEventListener('keydown', onListKeydown)
  offEsc = registerEsc(() => {
    if (searchOpen.value) {
      closeSearch()
      return true
    }
    return false
  })
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onListKeydown)
  offEsc?.()
})

// 渲染分组：纯函数（composables/messageGrouping.ts，有单测）——
// 工具卡并入助手回合块（思考 → 执行 → 回复），绝不冒充消息气泡、绝不双重渲染。
// 分组结果与窗口切片的组合见上面的 allItems / items（阶段 2）。
</script>

<style scoped>
/* 命中高亮闪烁：短促两下，不常驻（常驻底色会跟选中/气泡样式打架） */
.search-flash {
  animation: search-flash-anim 1.2s ease-out 1;
  border-radius: 12px;
}
@keyframes search-flash-anim {
  0%, 60% { box-shadow: 0 0 0 3px var(--c-primary-soft); background: var(--c-primary-soft); }
  100% { box-shadow: none; background: transparent; }
}
</style>

<template>
  <div class="relative flex min-h-0 flex-1 flex-col">
  <div
    ref="scroller"
    class="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4"
    role="log"
    aria-label="对话记录"
    data-conversation
    @scroll.passive="onScrollWindow"
  >
    <!-- 历史载入中：先于空态渲染（否则点开有历史的会话会闪一下"没有消息"） -->
    <div v-if="store.loadingSession" class="flex h-full items-center justify-center">
      <p class="text-sm text-[var(--c-text-dim)]">正在载入历史…</p>
    </div>

    <!-- 向上补更早的历史（0.3 尾屏优先）：加载中给一句可见反馈 -->
    <p v-else-if="store.loadingOlder" class="py-1 text-center text-[11px] text-[var(--c-text-faint)]" role="status">
      正在载入更早的消息…
    </p>

    <!-- 空态（0.3 改版）：一件事——模型、工作区、三条建议。不再堆灰卡片与一排药丸：
         开着就能读的三行字 + 三个安静的入口，视线上移即可开始。 -->
    <div v-else-if="!store.messages.length" class="flex h-full flex-col items-center justify-center gap-5 px-6">
      <p class="max-w-full truncate text-base font-medium text-[var(--c-text)]" title="当前模型（点击输入框左侧可切换）">
        {{ channels.activeModel || '未选择模型' }}
      </p>
      <p class="max-w-full truncate text-xs text-[var(--c-text-dim)]" :title="ws.path">
        {{
          ws.path
            ? `工作区 ${shortDir(ws.path)} · 可读写文件、跑命令、查 Git`
            : '纯对话 · 直接提问即可；贴图与传文件照常可用'
        }}
      </p>
      <div class="flex flex-col items-stretch gap-1 pt-1">
        <button
          v-for="s in suggestions"
          :key="s"
          class="rounded-lg px-4 py-1.5 text-center text-[13px] text-[var(--c-text-dim)] transition-colors hover:bg-[var(--c-surface-soft)] hover:text-[var(--c-primary)]"
          @click="emit('suggest', s)"
        >
          {{ s }}
        </button>
      </div>
    </div>

    <!-- 窗口切片（阶段 2）：items 只是完整分组的一段（尾部常挂），顺序与键完全不变。
         于是这个容器的**直接子元素**就是"当前挂了哪些回合"——窗口测试按它计数。
         为什么不给每个回合打 data-item-key：MessageBubble 是多根组件（气泡 + 灯箱），
         Vue 无法把透传属性落到某个根上，会出现"属性丢失"的假象。 -->
    <template v-for="item in items" :key="item.key">
      <!-- 搜索跳转锚点（0.0.19）：data-skey 供命中项 scrollIntoView + 闪烁。
           包一层普通 div（不能用 display:contents——无盒模型的元素滚不进去），
           space-y 仍按直接子元素计数，窗口测试不受影响。 -->
      <div :data-skey="item.key" class="min-w-0">
      <!-- 空卡守卫（0.0.06）：无标题/内容/diff 且非执行中的工具事件不出卡 -->
      <ToolCard
        v-if="item.kind === 'tool' && (item.m.title || item.m.content || item.m.diff || item.m.status === 'running')"
        :m="item.m"
      />
      <!-- kind === 'todo' 刻意不渲染：任务清单改由悬浮件（FloatingTodo 挂在对话面板上）
           呈现，不再随消息流滚走。数据仍在 store/账本里（重放与实时同源），只是换了载体 -->
      <ApprovalCard v-else-if="item.kind === 'approval'" :m="item.m" />
      <AskCard v-else-if="item.kind === 'ask'" :m="item.m" />
      <MessageBubble
        v-else-if="item.kind === 'turn'"
        :m="item.run[0]"
        :run="item.run"
        @rerun="emit('rerun', $event)"
      />
      <MessageBubble v-else-if="item.kind === 'single'" :m="item.m" @rerun="emit('rerun', $event)" />
      </div>
    </template>
  </div>

  <!-- 会话内搜索条（Ctrl+F，0.0.19）：浮在消息区顶部，不挤压布局 -->
  <div
    v-if="searchOpen"
    class="absolute left-1/2 top-2 z-20 flex w-[420px] max-w-[90%] -translate-x-1/2 items-center gap-2 rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] px-3 py-1.5 shadow-[var(--shadow-float)]"
    role="search"
  >
    <AppIcon name="search" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
    <input
      ref="searchInput"
      v-model="query"
      type="text"
      placeholder="在当前会话中搜索…"
      aria-label="会话内搜索"
      class="min-w-0 flex-1 bg-transparent text-xs text-[var(--c-text)] outline-none"
      @keydown.enter.prevent="moveHit($event.shiftKey ? -1 : 1)"
    />
    <span class="shrink-0 tabular-nums text-[10px] text-[var(--c-text-faint)]">
      {{ query.trim() ? (hits.length ? `${hitIndex + 1}/${hits.length}` : '无命中') : '' }}
    </span>
    <button class="btn-ghost shrink-0" :disabled="!hits.length" aria-label="上一个命中" @click="moveHit(-1)">
      <AppIcon name="chevron-down" :size="13" class="rotate-180" />
    </button>
    <button class="btn-ghost shrink-0" :disabled="!hits.length" aria-label="下一个命中" @click="moveHit(1)">
      <AppIcon name="chevron-down" :size="13" />
    </button>
    <button class="btn-ghost shrink-0" aria-label="关闭搜索" @click="closeSearch">
      <AppIcon name="x" :size="13" />
    </button>
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
