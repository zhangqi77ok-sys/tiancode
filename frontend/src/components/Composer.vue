<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useChannelStore } from '../stores/channels'
import { useChatStore } from '../stores/chat'
import AppIcon from './AppIcon.vue'

// 输入框自动增高上限（px）
const MAX_INPUT_HEIGHT_PX = 128

const store = useChatStore()
const channels = useChannelStore()
const draft = defineModel<string>({ required: true })
const box = ref<HTMLTextAreaElement | null>(null)

// ---- 模型选择器（0.2.28 用户反馈：这里的模型应该是已有的模型，可以支持选择）----
// 数据源 = 全部可用渠道的模型列表；切换 = 激活对应渠道并指定模型（后端 SetActiveModel）。
// 显示的模型即实际运行的模型（后端回传 defaultModel，不再各说各话）。
const modelMenuOpen = ref(false)
const modelMenuRef = ref<HTMLElement | null>(null)
const modelOptions = computed(() => channels.modelOptions)

function isCurrent(opt: { channelId: string; model: string }): boolean {
  return channels.activeChannel?.id === opt.channelId && channels.activeModel === opt.model
}

async function pickModel(opt: { channelId: string; model: string }) {
  modelMenuOpen.value = false
  if (isCurrent(opt)) return
  await channels.setActiveModel(opt.channelId, opt.model)
}

function onDocMousedown(e: MouseEvent) {
  if (!modelMenuOpen.value) return
  const el = modelMenuRef.value
  if (el && !el.contains(e.target as Node)) modelMenuOpen.value = false
}

onMounted(() => document.addEventListener('mousedown', onDocMousedown))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocMousedown))

// 自动增高且不超过上限；发送后复位高度
function autoGrow(e: Event) {
  const t = e.target as HTMLTextAreaElement
  t.style.height = 'auto'
  t.style.height = Math.min(t.scrollHeight, MAX_INPUT_HEIGHT_PX) + 'px'
}

// Enter 发送 / Shift+Enter 换行（ChatGPT/Cursor/Cline 通用惯例，替代旧版 Ctrl+Enter）。
// isComposing 保护：中文输入法选词时的 Enter 是候选确认，不是发送意图——必须放行。
function onComposerKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    void submit()
  }
}

function resetBox() {
  if (box.value) box.value.style.height = 'auto'
}

async function submit() {
  const text = draft.value.trim()
  if (!text) return
  if (store.running) {
    // 回合进行中：入队（终态后自动依次发出），不再拒绝提交（0.2.14）
    store.enqueue(text)
    draft.value = ''
    resetBox()
    return
  }
  draft.value = ''
  resetBox()
  await store.send(text)
}

// 队列编辑：取回文本并出队（焦点回到输入框继续改）
function editQueued(id: number) {
  const t = store.editQueued(id)
  if (t !== undefined) {
    draft.value = t
    box.value?.focus()
  }
}
</script>

<template>
  <div data-composer class="border-t border-[var(--c-border)] p-4">
    <!-- 模型选择器（0.2.28）：列出全部可用渠道的模型，点击切换；当前项标记"当前" -->
    <div ref="modelMenuRef" class="relative mb-2">
      <button
        class="flex items-center gap-1.5 px-1 text-[11px] text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-text-dim)]"
        aria-haspopup="menu"
        :aria-expanded="modelMenuOpen"
        :title="modelOptions.length ? '点击切换模型（来自已配置的渠道）' : '尚未配置模型：请在侧栏底部打开「渠道管理」'"
        @click="modelMenuOpen = !modelMenuOpen"
      >
        {{ channels.activeModel ? `模型 ${channels.activeModel}` : '未选择模型' }}
        <template v-if="channels.activeChannel"> · {{ channels.activeChannel.name }}</template>
        <AppIcon name="chevron-down" :size="10" />
      </button>
      <div
        v-if="modelMenuOpen"
        role="menu"
        class="absolute bottom-full left-0 z-40 mb-1 max-h-72 w-80 overflow-y-auto rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] p-1.5 shadow-lg"
      >
        <div class="px-2 py-1 text-[11px] text-[var(--c-text-faint)]">选择模型（来自已配置的渠道）</div>
        <p v-if="!modelOptions.length" class="px-2 py-1.5 text-xs text-[var(--c-text-dim)]">
          还没有可用模型——请在侧栏底部打开「渠道管理」添加渠道与模型
        </p>
        <button
          v-for="opt in modelOptions"
          :key="`${opt.channelId}::${opt.model}`"
          role="menuitem"
          class="menu-item"
          @click="pickModel(opt)"
        >
          <AppIcon name="message" :size="12" class="shrink-0 text-[var(--c-text-faint)]" />
          <span class="min-w-0 flex-1 truncate text-left">{{ opt.model }}</span>
          <span class="max-w-[8rem] shrink-0 truncate text-[10px] text-[var(--c-text-faint)]">{{ opt.channelName }}</span>
          <span v-if="isCurrent(opt)" class="shrink-0 text-[10px] text-[var(--c-primary)]">当前</span>
        </button>
      </div>
    </div>
    <!-- 输入队列：进行中提交的待发消息；立即发送 = 置顶，本轮结束最先发出 -->
    <div v-if="store.queue.length" class="mb-2 space-y-1" aria-label="输入队列">
      <div class="flex items-center gap-1.5 px-1 text-[11px] text-[var(--c-text-faint)]">
        <AppIcon name="message" :size="11" /> 队列 ({{ store.queue.length }})
      </div>
      <div
        v-for="q in store.queue"
        :key="q.id"
        class="flex items-center gap-1.5 rounded-lg border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-2.5 py-1.5 text-xs"
      >
        <span class="min-w-0 flex-1 truncate text-[var(--c-text-dim)]" :title="q.text">{{ q.text }}</span>
        <button
          class="shrink-0 rounded p-1 text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-primary)]"
          title="立即发送（本轮结束后最先发出）"
          aria-label="置顶该消息"
          @click="store.promoteQueued(q.id)"
        >
          <AppIcon name="send" :size="12" />
        </button>
        <button
          class="shrink-0 rounded p-1 text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-primary)]"
          title="取回编辑"
          aria-label="编辑该消息"
          @click="editQueued(q.id)"
        >
          <AppIcon name="pencil" :size="12" />
        </button>
        <button
          class="shrink-0 rounded p-1 text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-err-text)]"
          title="删除"
          aria-label="删除该消息"
          @click="store.removeQueued(q.id)"
        >
          <AppIcon name="trash" :size="12" />
        </button>
      </div>
    </div>

    <div class="flex items-end gap-3">
      <textarea
        ref="box"
        v-model="draft"
        rows="1"
        aria-label="消息输入框"
        class="max-h-32 min-w-0 flex-1 resize-none rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-4 py-2.5 text-sm leading-6 transition-colors focus:border-[var(--c-primary)]"
        placeholder="输入消息…（Enter 发送，Shift+Enter 换行；回合进行中自动排队）"
        @keydown="onComposerKeydown"
        @input="autoGrow"
      ></textarea>
      <button
        v-if="store.running"
        type="button"
        class="btn-primary h-10 shrink-0 gap-2 px-4 text-sm"
        :disabled="store.stopping"
        @click="store.stop()"
      >
        <AppIcon name="stop" :size="14" /> {{ store.stopping ? '正在中断' : '中断' }}
      </button>
      <button
        v-else
        class="btn-icon shrink-0 disabled:cursor-not-allowed disabled:opacity-50"
        title="发送（Enter）"
        aria-label="发送"
        :disabled="!draft.trim()"
        @click="submit"
      >
        <AppIcon name="send" :size="16" />
      </button>
    </div>
  </div>
</template>
