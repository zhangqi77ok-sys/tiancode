<script setup lang="ts">
import { computed, provide, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useChatStore, type TodoItem } from './stores/chat'
import { useCatalogStore } from './stores/catalog'
import { useToast } from './composables/useToast'
import { bridge } from './wails'
import AppHeader from './components/AppHeader.vue'
import ChannelSettings from './components/ChannelSettings.vue'
import Composer from './components/Composer.vue'
import DialogHost from './components/DialogHost.vue'
import FloatingTodo from './components/FloatingTodo.vue'
import McpSettings from './components/McpSettings.vue'
import MessageList from './components/MessageList.vue'
import SessionList from './components/SessionList.vue'
import SkillSettings from './components/SkillSettings.vue'
import ToastHost from './components/ToastHost.vue'
import TurnReview from './components/TurnReview.vue'

// 根组件退化为布局壳：顶栏/侧栏/对话/输入各自自治，事件桥在此统一接线。
const store = useChatStore()
const { push: toast } = useToast()

// 会话操作的失败原来只写在侧栏最底部，容易被挡住——通知先冒出来。
// toast 后清空 error：否则同一文案连续出现时 watch 不再触发（第二次静默无声）。
watch(
  () => store.error,
  (msg) => {
    if (!msg) return
    toast('error', msg)
    store.error = ''
  },
)

const channelsOpen = ref(false)
const mcpOpen = ref(false)
const skillsOpen = ref(false)
// 模态守卫收敛一处：新增模态只需在这里登记（此前用三个布尔枚举，新增必漏）
const anyModalOpen = computed(() => channelsOpen.value || mcpOpen.value || skillsOpen.value)

// 输入草稿**按会话各存一份**：此前是全局单例——在 A 里敲的半句话切到 B
// 回车就发进了 B（串会话），A 的草稿也随之丢失。切换/新建时草稿各归各位。
const drafts = ref<Record<string, string>>({})
const draft = computed({
  get: () => drafts.value[store.sessionId] ?? '',
  set: (v: string) => {
    drafts.value[store.sessionId] = v
  },
})

// 模态打开函数（0.0.06：入口统一在侧栏底部，汉堡导航已下线）
function openChannels() {
  channelsOpen.value = true
}
function openMcp() {
  mcpOpen.value = true
}
function openSkills() {
  skillsOpen.value = true
}

// ---- 快捷键（0.0.06）----
//   Ctrl/Cmd+N 新建对话；Ctrl/Cmd+I 或 Ctrl/Cmd+/ 聚焦输入框；
//   Ctrl/Cmd+↑/↓ 上一条/下一条会话；Esc：模态打开时不抢（BaseModal 捕获层处理），
//   否则中断生成。全部在模态打开时停用（不与模态内输入抢键）。
const composerInput = ref<HTMLTextAreaElement | null>(null)
// Composer 注册它的 textarea（快捷键聚焦用）：函数注入，避免类型耦合
provide('registerComposerInput', (el: HTMLTextAreaElement | null) => {
  composerInput.value = el
})

function sessionSiblings(): string[] {
  // 侧栏顺序即会话顺序：按空间分组后的扁平 id 列表（含当前会话）
  const ids = store.summaries.map((s) => s.id)
  const inList = store.sessionId && ids.includes(store.sessionId)
  if (!inList && store.sessionId) return [store.sessionId, ...ids]
  return ids
}

function cycleSession(delta: number) {
  const ids = sessionSiblings()
  if (ids.length === 0) return
  const cur = ids.indexOf(store.sessionId)
  const next = cur < 0 ? (delta > 0 ? 0 : ids.length - 1) : (cur + delta + ids.length) % ids.length
  void store.selectSession(ids[next])
}

function onGlobalKeydown(e: KeyboardEvent) {
  if (anyModalOpen.value) return // 模态打开：不抢任何全局键（Esc 归模态）
  const mod = e.ctrlKey || e.metaKey
  if (e.key === 'Escape') {
    if (store.running) {
      e.preventDefault()
      store.stop()
    }
    return
  }
  if (!mod) return
  const k = e.key.toLowerCase()
  if (k === 'n') {
    e.preventDefault()
    void store.newSession()
  } else if (k === 'i' || k === '/') {
    e.preventDefault()
    composerInput.value?.focus()
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    cycleSession(-1)
  } else if (e.key === 'ArrowDown') {
    e.preventDefault()
    cycleSession(1)
  }
}

onMounted(() => {
  window.addEventListener('keydown', onGlobalKeydown)
  // 先接线再 init：流式事件由 store 的会话守卫（sessionID 比对）兜住，不会串会话
  bridge().runtime.EventsOn('chat:chunk', (p: { sessionID: string; delta: string; thinking: string }) => {
    store.onChunk(p)
  })
  bridge().runtime.EventsOn('chat:terminal', (p: { sessionID: string; endReason: number; error: string }) => {
    store.onTerminal(p)
  })
  // 油表（0.0.09）：本轮 prompt token（上下文大小读数），顶栏显示
  bridge().runtime.EventsOn(
    'chat:usage',
    (p: { sessionID: string; prompt: number; completion: number; total: number }) => {
      store.onUsage(p)
    },
  )
  bridge().runtime.EventsOn(
    'chat:tool',
    (p: {
      sessionID: string
      name: string
      status: string
      summary: string
      content?: string
      diff?: string
      title?: string
      op?: string
      callID?: string
      hasUndo?: boolean
      undoPath?: string
      undoNote?: string
    }) => {
      store.onTool(p)
    },
  )
  // 审批卡片：sessionID 必填（后端必推）——缺标识的卡片宁可丢弃并报错，
  // 也绝不插进当前视图（那正是"数据串会话"）
  bridge().runtime.EventsOn(
    'chat:approval',
    (p: { id: string; sessionID: string; sessionTitle?: string; toolName: string; arguments: string }) => {
      store.onApproval(p)
    },
  )
  bridge().runtime.EventsOn('chat:todo', (p: { sessionID: string; items: { text: string; status: string }[] }) => {
    store.onTodo({ sessionID: p.sessionID, items: p.items as TodoItem[] })
  })
  // 问答卡：sessionID 必填（与审批同款，0.2.25 多会话）
  bridge().runtime.EventsOn(
    'chat:ask',
    (p: { id: string; sessionID: string; question: string; options?: string[] }) => {
      store.onAsk(p)
    },
  )
  void store.init()
  void useCatalogStore().load()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onGlobalKeydown)
})
</script>

<template>
  <div class="flex h-screen flex-col gap-4 p-4 md:p-5">
    <AppHeader />

    <div class="flex min-h-0 flex-1 gap-4">
      <SessionList
        @open-channels="openChannels"
        @open-mcp="openMcp"
        @open-skills="openSkills"
      />

      <main class="card relative flex min-w-0 flex-1 flex-col">
        <MessageList @suggest="draft = $event" />
        <!-- 本轮变更审查带（0.0.09）：write/edit 收拢在输入框上方，点开即 diff -->
        <TurnReview />
        <Composer v-model="draft" />
        <!-- 悬浮任务清单：挂在对话面板内（absolute 以面板为参照系），位置/折叠态跨重启保留 -->
        <FloatingTodo />
      </main>
    </div>

    <!-- 全局宿主：渠道管理 + 对话框 + 通知（各挂一个） -->
    <ChannelSettings v-if="channelsOpen" @close="channelsOpen = false" />
    <McpSettings v-if="mcpOpen" @close="mcpOpen = false" />
    <SkillSettings v-if="skillsOpen" @close="skillsOpen = false" />
    <DialogHost />
    <ToastHost />
  </div>
</template>
