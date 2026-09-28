<script setup lang="ts">
import { ref } from 'vue'
import { useChatStore } from '../stores/chat'
import AppIcon from './AppIcon.vue'

// 输入框自动增高上限（px）
const MAX_INPUT_HEIGHT_PX = 128

const store = useChatStore()
const draft = defineModel<string>({ required: true })
const box = ref<HTMLTextAreaElement | null>(null)

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
  <div class="border-t border-[var(--c-border)] p-4">
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
      <button v-if="store.running" class="btn-primary h-10 shrink-0 gap-2 px-4 text-sm" @click="store.stop()">
        <AppIcon name="stop" :size="14" /> 中断
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
