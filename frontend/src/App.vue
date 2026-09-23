<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useChatStore } from './stores/chat'
import { useChannelStore } from './stores/channels'
import ChannelSettings from './components/ChannelSettings.vue'
import { bridge } from './wails'

// 根组件：左侧会话卡片 + 右侧对话卡片。
// 视觉语言：浅色柔和底 + 白卡 + 紫罗兰主色 + 药丸控件（令牌见 style.css）。
const store = useChatStore()
const channels = useChannelStore()
const input = ref('')
const scroller = ref<HTMLElement | null>(null)
const settingsOpen = ref(false)
const workspace = ref('')

// 顶栏只显示末级目录名（完整路径太长会挤掉状态区）
const workspaceName = computed(() => workspace.value.split(/[\\/]/).filter(Boolean).pop() ?? '未设置工作区')

// 切换工作区：工具受控根随即重建（后端契约：非法路径返回错误，不静默保留旧值）
async function switchWorkspace() {
  const next = window.prompt('工作区路径（工具只能读写此目录内）', workspace.value)
  if (!next) return
  try {
    await bridge().app.SetWorkspace(next)
    workspace.value = (await bridge().app.GetWorkspace()) ?? ''
  } catch (e) {
    window.alert(String(e instanceof Error ? e.message : e))
  }
}

// 顶栏展示当前默认渠道：没有渠道时给出明确引导（而不是让用户对着发送键发呆）
const activeChannelName = computed(
  () => channels.list.find((c) => c.active)?.name ?? '未配置渠道',
)
const hasChannel = computed(() => channels.list.length > 0 && !!channels.activeId)

// 建议提示（参考图的 chip 行）：点击填入输入框
const suggestions = ['介绍这个项目', '读取 go.mod 前 5 行并复述 module 名', '运行 go test ./internal/core/tools/']

async function scrollToBottom() {
  await nextTick()
  scroller.value?.scrollTo({ top: scroller.value.scrollHeight })
}
watch(() => store.messages.length, scrollToBottom)

function fmtTime(at?: number): string {
  if (!at) return ''
  return new Date(at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

async function submit() {
  const text = input.value.trim()
  if (!text || store.running) return
  input.value = ''
  await store.send(text)
}

function stop() {
  store.stop()
}

// 重命名会话：标题写入账本（重启后仍在）；取消（null）视为放弃
async function renameSession(id: string) {
  if (store.running) return
  const next = window.prompt('会话名称（最多 60 字）', store.titleOf(id))
  if (next === null) return
  await store.renameSession(id, next)
}

// 删除会话：不可逆操作，先确认（消息删除后无法从界面找回）
async function removeSession(id: string) {
  if (store.running) return
  if (!window.confirm(`删除会话「${id}」？该会话的全部消息将被移除`)) return
  await store.removeSession(id)
}

onMounted(async () => {
  await store.init()
  await channels.load()
  workspace.value = (await bridge().app.GetWorkspace()) ?? ''
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
  <div class="flex h-screen flex-col gap-4 p-5">
    <!-- 顶栏 -->
    <header class="flex items-center justify-between">
      <div class="flex items-baseline gap-3">
        <h1 class="text-[20px] font-semibold tracking-tight">tiancode</h1>
        <span class="text-xs text-[var(--c-text-dim)]">桌面 AI 编程智能体</span>
      </div>
      <div class="flex items-center gap-2">
        <span class="chip">{{ store.sessions.length }} 个会话</span>
        <button class="chip" title="切换工作区" @click="switchWorkspace">▣ {{ workspaceName }}</button>
        <button
          class="chip"
          :class="hasChannel ? '' : 'text-[var(--c-warn)] border-[var(--c-warn)]'"
          title="模型渠道设置"
          @click="settingsOpen = true"
        >
          {{ hasChannel ? activeChannelName : '未配置渠道 · 点击设置' }}
        </button>
        <span class="chip" :class="store.running ? 'text-[var(--c-primary)] border-[var(--c-primary)]' : ''">
          {{ store.running ? '运行中' : '空闲' }}
        </span>
      </div>
    </header>

    <div class="flex min-h-0 flex-1 gap-4">
      <!-- 会话列表 -->
      <aside class="card flex w-60 shrink-0 flex-col p-3">
        <button class="btn-primary mb-3 w-full py-2 text-sm" @click="store.newSession()">＋ 新建对话</button>
        <div class="flex-1 space-y-1 overflow-y-auto">
          <div v-for="id in store.sessions" :key="id" class="group flex items-center gap-1">
            <button
              class="min-w-0 flex-1 truncate rounded-xl px-3 py-2 text-left text-sm transition-colors"
              :class="
                id === store.sessionId
                  ? 'bg-[var(--c-primary-soft)] font-medium text-[var(--c-primary)]'
                  : 'text-[var(--c-text-dim)] hover:bg-[var(--c-surface-soft)]'
              "
              @click="store.selectSession(id)"
            >
              {{ store.titleOf(id) }}
            </button>
            <button
              class="shrink-0 rounded-lg px-1.5 py-1 text-xs text-[var(--c-text-faint)] opacity-0 transition-opacity hover:text-[var(--c-primary)] group-hover:opacity-100"
              :disabled="store.running"
              title="重命名会话"
              @click="renameSession(id)"
            >
              ✎
            </button>
            <button
              class="shrink-0 rounded-lg px-1.5 py-1 text-xs text-[var(--c-text-faint)] opacity-0 transition-opacity hover:text-[var(--c-err)] group-hover:opacity-100"
              :disabled="store.running"
              title="删除会话"
              @click="removeSession(id)"
            >
              ✕
            </button>
          </div>
        </div>
        <div v-if="store.error" class="mt-2 px-1 text-[11px] text-[var(--c-err)]">{{ store.error }}</div>
        <div v-else class="mt-2 px-1 text-[11px] text-[var(--c-text-faint)]">历史由事件账本恢复</div>
      </aside>

      <!-- 对话区 -->
      <main class="card flex min-w-0 flex-1 flex-col">
        <div ref="scroller" class="flex-1 space-y-4 overflow-y-auto px-5 py-4">
          <!-- 空状态 + 建议 chips -->
          <div v-if="!store.messages.length" class="flex h-full flex-col items-center justify-center gap-4">
            <p class="text-sm text-[var(--c-text-dim)]">发一条消息开始，或试试：</p>
            <div class="flex flex-wrap justify-center gap-2">
              <button v-for="s in suggestions" :key="s" class="chip" @click="input = s">{{ s }}</button>
            </div>
          </div>

          <template v-for="(m, i) in store.messages" :key="i">
            <!-- 工具卡片：药丸 + 状态点 -->
            <div v-if="m.role === 'tool'" class="flex justify-start">
              <div
                class="inline-flex items-center gap-2 rounded-full border px-3 py-1.5 text-xs"
                :class="
                  m.status === 'error'
                    ? 'border-[var(--c-err)] bg-[var(--c-err-soft)] text-[var(--c-err)]'
                    : 'border-[var(--c-border)] bg-[var(--c-ok-soft)] text-[var(--c-text-dim)]'
                "
              >
                <span class="h-1.5 w-1.5 rounded-full" :class="m.status === 'error' ? 'bg-[var(--c-err)]' : 'bg-[var(--c-ok)]'"></span>
                <span class="font-medium text-[var(--c-text)]">{{ m.toolName }}</span>
                <span class="max-w-[420px] truncate">{{ m.content }}</span>
              </div>
            </div>

            <!-- 用户消息：紫罗兰实心气泡，右对齐 -->
            <div v-else-if="m.role === 'user'" class="flex flex-col items-end gap-1">
              <div class="flex items-center gap-2 text-[11px] text-[var(--c-text-faint)]">
                <span>YOU</span><span>{{ fmtTime(m.at) }}</span>
              </div>
              <div
                class="max-w-[75%] whitespace-pre-wrap rounded-2xl bg-[var(--c-primary)] px-4 py-2.5 text-sm leading-6 text-white"
              >
                {{ m.content }}
              </div>
            </div>

            <!-- 助手消息：白卡 + 角色标签 -->
            <div v-else class="flex flex-col items-start gap-1">
              <div class="flex items-center gap-2 text-[11px] text-[var(--c-text-faint)]">
                <span class="font-medium text-[var(--c-text-dim)]">AGENT</span><span>{{ fmtTime(m.at) }}</span>
              </div>
              <div
                class="max-w-[85%] whitespace-pre-wrap rounded-2xl border px-4 py-3 text-sm leading-6"
                :class="
                  m.term === 3 || m.term === 4
                    ? 'border-[var(--c-warn)] bg-[var(--c-warn-soft)]'
                    : m.error
                      ? 'border-[var(--c-err)] bg-[var(--c-err-soft)]'
                      : 'border-[var(--c-border)] bg-[var(--c-surface)]'
                "
              >
                {{ m.content }}<span v-if="m.streaming" class="caret"></span>
              </div>
            </div>
          </template>
        </div>

        <!-- 输入区 -->
        <div class="flex items-end gap-3 border-t border-[var(--c-border)] p-4">
          <textarea
            v-model="input"
            rows="1"
            class="max-h-32 flex-1 resize-none rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-4 py-2.5 text-sm leading-6 outline-none transition-colors focus:border-[var(--c-primary)]"
            placeholder="输入消息…（Ctrl+Enter 发送）"
            @keydown.ctrl.enter.prevent="submit"
            @input="
              (e) => {
                const t = e.target as HTMLTextAreaElement
                t.style.height = 'auto'
                t.style.height = Math.min(t.scrollHeight, 128) + 'px'
              }
            "
          ></textarea>
          <button v-if="store.running" class="btn-primary h-10 px-4 text-sm" @click="stop">中断</button>
          <button v-else class="btn-icon shrink-0" title="发送" @click="submit">➤</button>
        </div>
      </main>
    </div>

    <!-- 渠道设置（模态）：保存/切换后顶栏渠道名随之更新 -->
    <ChannelSettings v-if="settingsOpen" @close="settingsOpen = false" />
  </div>
</template>
