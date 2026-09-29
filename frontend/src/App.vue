<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useChatStore, type TodoItem } from './stores/chat'
import { useCatalogStore } from './stores/catalog'
import { useToast } from './composables/useToast'
import { bridge } from './wails'
import AppHeader from './components/AppHeader.vue'
import AppNav from './components/AppNav.vue'
import ChannelSettings from './components/ChannelSettings.vue'
import Composer from './components/Composer.vue'
import DialogHost from './components/DialogHost.vue'
import FloatingTodo from './components/FloatingTodo.vue'
import McpSettings from './components/McpSettings.vue'
import MessageList from './components/MessageList.vue'
import SessionList from './components/SessionList.vue'
import SkillSettings from './components/SkillSettings.vue'
import ToastHost from './components/ToastHost.vue'

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

const navOpen = ref(false) // logo 左侧唯一按钮拉出的导航栏
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

// Esc 中断生成（Claude/ChatGPT 惯例）。模态（渠道设置/对话框）打开时，
// BaseModal 在捕获层拦截 Esc 并停止传播，这里的冒泡监听不会误触发。
function openChannels() {
  channelsOpen.value = true
  navOpen.value = false
}
function openMcp() {
  mcpOpen.value = true
  navOpen.value = false
}
function openSkills() {
  skillsOpen.value = true
  navOpen.value = false
}

function onGlobalKeydown(e: KeyboardEvent) {
  if (e.key !== 'Escape' || anyModalOpen.value) return
  // Esc 优先关导航（此前运行中会先去中断生成，导航反而关不掉）
  if (navOpen.value) {
    navOpen.value = false
    return
  }
  if (store.running) {
    e.preventDefault()
    store.stop()
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
    }) => {
      store.onTool(p)
    },
  )
  // 审批卡片：sessionID 必填（后端必推）——缺标识的卡片宁可丢弃并报错，
  // 也绝不插进当前视图（那正是"数据串会话"）
  bridge().runtime.EventsOn(
    'chat:approval',
    (p: { id: string; sessionID: string; toolName: string; arguments: string }) => {
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
    <AppHeader
      :nav-open="navOpen"
      @toggle-nav="navOpen = !navOpen"
      @open-channels="openChannels"
    />

    <div class="flex min-h-0 flex-1 gap-4">
      <div
        v-if="navOpen"
        class="fixed inset-0 z-30 bg-black/25"
        @click="navOpen = false"
      ></div>

      <SessionList @open-channels="openChannels" />
      <AppNav
        :open="navOpen"
        @close="navOpen = false"
        @open-channels="openChannels"
        @open-mcp="openMcp"
        @open-skills="openSkills"
      />

      <main class="card relative flex min-w-0 flex-1 flex-col">
        <MessageList @suggest="draft = $event" />
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
