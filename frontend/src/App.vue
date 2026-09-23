<script setup lang="ts">
import { nextTick, onMounted, ref, watch } from 'vue'
import { useChatStore } from './stores/chat'
import { bridge } from './wails'

// 根组件：左会话列表 + 右对话区（流式气泡/终态标签/中断按钮）。
// 设计令牌见 style.css；全部交互围绕"可区分的三终态"。
const store = useChatStore()
const input = ref('')
const scroller = ref<HTMLElement | null>(null)

async function scrollToBottom() {
  await nextTick()
  scroller.value?.scrollTo({ top: scroller.value.scrollHeight })
}
watch(() => store.messages.length, scrollToBottom)

async function submit() {
  const text = input.value.trim()
  if (!text || store.running) return
  input.value = ''
  await store.send(text)
}

function stop() {
  store.stop()
}

onMounted(async () => {
  await store.init()
  // 事件桥：流式块与终态（Observer，ADR-0005）
  bridge().runtime.EventsOn('chat:chunk', (p: { sessionID: string; delta: string; thinking: string }) => {
    store.onChunk(p)
    void scrollToBottom()
  })
  bridge().runtime.EventsOn('chat:terminal', (p: { sessionID: string; endReason: number; error: string }) => {
    store.onTerminal(p)
  })
  bridge().runtime.EventsOn('chat:tool', (p: { sessionID: string; name: string; status: string; summary: string }) => {
    store.onTool(p)
  })
})
</script>

<template>
  <div class="flex h-screen bg-[#0D1117] text-[#E6EDF3]">
    <!-- 侧栏：会话列表 -->
    <aside class="flex w-56 shrink-0 flex-col border-r border-[#21262D]">
      <div class="p-3">
        <button
          class="w-full rounded-md bg-[#3B82F6] py-1.5 text-sm font-medium transition-colors hover:bg-[#2563EB]"
          @click="store.newSession()"
        >
          ＋ 新建会话
        </button>
      </div>
      <div class="flex-1 space-y-1 overflow-y-auto px-2 pb-2">
        <button
          v-for="id in store.sessions"
          :key="id"
          class="w-full truncate rounded px-2 py-1.5 text-left text-sm transition-colors"
          :class="id === store.sessionId ? 'bg-[#161B22] text-[#E6EDF3]' : 'text-[#8B949E] hover:bg-[#161B22]'"
          @click="store.selectSession(id)"
        >
          {{ id }}
        </button>
      </div>
      <div class="border-t border-[#21262D] px-3 py-2 text-[11px] text-[#8B949E]">
        {{ store.sessions.length }} 个会话
      </div>
    </aside>

    <!-- 主区：对话 -->
    <main class="flex min-w-0 flex-1 flex-col">
      <div ref="scroller" class="flex-1 space-y-3 overflow-y-auto px-6 py-4">
        <div v-if="!store.messages.length" class="flex h-full items-center justify-center text-sm text-[#8B949E]">
          发送第一条消息开始对话
        </div>
        <div
          v-for="(m, i) in store.messages"
          :key="i"
          class="flex"
          :class="m.role === 'user' ? 'justify-end' : 'justify-start'"
        >
          <!-- 工具卡片：名称 + 状态点 + 摘要 -->
          <div
            v-if="m.role === 'tool'"
            class="rounded-lg border px-3 py-1.5 text-xs"
            :class="m.status === 'error' ? 'border-[#F85149]/60' : 'border-[#3FB950]/40'"
          >
            <span class="font-medium text-[#E6EDF3]">{{ m.toolName }}</span>
            <span class="mx-1.5" :class="m.status === 'error' ? 'text-[#F85149]' : 'text-[#3FB950]'">● {{ m.status }}</span>
            <span class="text-[#8B949E]">{{ m.content }}</span>
          </div>
          <div
            v-else
            class="max-w-[80%] whitespace-pre-wrap rounded-xl px-3.5 py-2.5 text-sm leading-6"
            :class="[
              m.role === 'user'
                ? 'bg-[#3B82F6] text-white'
                : 'border border-[#21262D] bg-[#161B22]',
              m.error ? 'border-[#F85149]/60' : '',
            ]"
          >
            {{ m.content }}<span
              v-if="m.streaming"
              class="ml-1 inline-block h-3.5 w-1.5 animate-pulse rounded-sm bg-[#8B949E] align-middle"
            ></span>
          </div>
        </div>
      </div>

      <!-- 输入区 -->
      <div class="flex gap-2 border-t border-[#21262D] p-3">
        <textarea
          v-model="input"
          rows="2"
          class="flex-1 resize-none rounded-lg border border-[#21262D] bg-[#161B22] px-3 py-2 text-sm outline-none transition-colors focus:border-[#3B82F6]"
          placeholder="输入消息…（Ctrl+Enter 发送）"
          @keydown.ctrl.enter.prevent="submit"
        ></textarea>
        <button
          v-if="store.running"
          class="rounded-lg bg-[#D29922] px-4 text-sm font-medium text-[#0D1117] transition-colors hover:bg-[#D29922]/85"
          @click="stop"
        >
          中断
        </button>
        <button
          v-else
          class="rounded-lg bg-[#3B82F6] px-4 text-sm font-medium transition-colors hover:bg-[#2563EB]"
          @click="submit"
        >
          发送
        </button>
      </div>
    </main>
  </div>
</template>
