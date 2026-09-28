<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useChatStore } from './stores/chat'
import { bridge } from './wails'
import AppHeader from './components/AppHeader.vue'
import Composer from './components/Composer.vue'
import DialogHost from './components/DialogHost.vue'
import MessageList from './components/MessageList.vue'
import SessionList from './components/SessionList.vue'
import ToastHost from './components/ToastHost.vue'

// 根组件退化为布局壳：顶栏/侧栏/对话/输入各自自治，事件桥在此统一接线。
const store = useChatStore()

const drawerOpen = ref(false) // 窄屏会话抽屉
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
  void store.init()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onGlobalKeydown)
})
</script>

<template>
  <div class="flex h-screen flex-col gap-4 p-4 md:p-5">
    <AppHeader @toggle-drawer="drawerOpen = !drawerOpen" />

    <div class="flex min-h-0 flex-1 gap-4">
      <!-- 窄屏抽屉遮罩：点击关闭（层级低于抽屉） -->
      <div
        v-if="drawerOpen"
        class="fixed inset-0 z-30 bg-black/25 md:hidden"
        @click="drawerOpen = false"
      ></div>

      <SessionList :open="drawerOpen" @close="drawerOpen = false" />

      <main class="card flex min-w-0 flex-1 flex-col">
        <MessageList @suggest="draft = $event" />
        <Composer v-model="draft" />
      </main>
    </div>

    <!-- 全局宿主：对话框 + 通知（各挂一个） -->
    <DialogHost />
    <ToastHost />
  </div>
</template>
