<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useChatStore, type TodoItem } from './stores/chat'
import { bridge } from './wails'
import AppHeader from './components/AppHeader.vue'
import ChannelSettings from './components/ChannelSettings.vue'
import Composer from './components/Composer.vue'
import DialogHost from './components/DialogHost.vue'
import FloatingTodo from './components/FloatingTodo.vue'
import MessageList from './components/MessageList.vue'
import SessionList from './components/SessionList.vue'
import ToastHost from './components/ToastHost.vue'

// 根组件退化为布局壳：顶栏/侧栏/对话/输入各自自治，事件桥在此统一接线。
const store = useChatStore()

const drawerOpen = ref(false) // 窄屏会话抽屉
const channelsOpen = ref(false) // 渠道管理面板（顶栏 chip 与侧栏底部入口共用同一面板）
const draft = ref('') // 输入草稿：建议 chips 回填、Composer 双向绑定

// Esc 中断生成（Claude/ChatGPT 惯例）。模态（渠道设置/对话框）打开时，
// BaseModal 在捕获层拦截 Esc 并停止传播，这里的冒泡监听不会误触发。
function onGlobalKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && store.running) {
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
  bridge().runtime.EventsOn('chat:approval', (p: { id: string; toolName: string; arguments: string }) => {
    store.onApproval(p)
  })
  bridge().runtime.EventsOn('chat:todo', (p: { sessionID: string; items: { text: string; status: string }[] }) => {
    store.onTodo({ sessionID: p.sessionID, items: p.items as TodoItem[] })
  })
  // 问答卡：ask_user 载荷不带 sessionID（流式中禁止切换会话，卡片必属当前会话）
  bridge().runtime.EventsOn('chat:ask', (p: { id: string; question: string; options?: string[] }) => {
    store.onAsk(p)
  })
  void store.init()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onGlobalKeydown)
})
</script>

<template>
  <div class="flex h-screen flex-col gap-4 p-4 md:p-5">
    <AppHeader
      @toggle-drawer="drawerOpen = !drawerOpen"
      @open-channels="channelsOpen = true"
    />

    <div class="flex min-h-0 flex-1 gap-4">
      <!-- 窄屏抽屉遮罩：点击关闭（层级低于抽屉） -->
      <div
        v-if="drawerOpen"
        class="fixed inset-0 z-30 bg-black/25 md:hidden"
        @click="drawerOpen = false"
      ></div>

      <SessionList
        :open="drawerOpen"
        @close="drawerOpen = false"
        @open-channels="channelsOpen = true"
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
    <DialogHost />
    <ToastHost />
  </div>
</template>
