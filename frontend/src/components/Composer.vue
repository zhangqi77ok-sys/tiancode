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

async function submit() {
  const text = draft.value.trim()
  if (!text || store.running) return
  draft.value = ''
  if (box.value) box.value.style.height = 'auto'
  await store.send(text)
}
</script>

<template>
  <div class="flex items-end gap-3 border-t border-[var(--c-border)] p-4">
    <textarea
      ref="box"
      v-model="draft"
      rows="1"
      aria-label="消息输入框"
      class="max-h-32 min-w-0 flex-1 resize-none rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-4 py-2.5 text-sm leading-6 transition-colors focus:border-[var(--c-primary)]"
      placeholder="输入消息…（Ctrl+Enter 发送）"
      @keydown.ctrl.enter.prevent="submit"
      @input="autoGrow"
    ></textarea>
    <button v-if="store.running" class="btn-primary h-10 shrink-0 gap-2 px-4 text-sm" @click="store.stop()">
      <AppIcon name="stop" :size="14" /> 中断
    </button>
    <button
      v-else
      class="btn-icon shrink-0 disabled:cursor-not-allowed disabled:opacity-50"
      title="发送（Ctrl+Enter）"
      aria-label="发送"
      :disabled="!draft.trim()"
      @click="submit"
    >
      <AppIcon name="send" :size="16" />
    </button>
  </div>
</template>
