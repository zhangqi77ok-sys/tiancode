<script setup lang="ts">
import { ref } from 'vue'
import type { ChatMsg } from '../stores/chat'
import { useChatStore } from '../stores/chat'
import AppIcon from './AppIcon.vue'

// 问答卡（ask_user）：待答态展示问题 + 选项按钮；已答态展示所选答案。
// 答复走 store：失败必须可见（error 位），成功后卡片转已答态防重复点击。
// 无选项时给自由输入（0.0.11）：此前无选项卡只有一个问题文本、没有任何出口，
// 模型那一轮会被永久挂住（ask 阻塞等答复，用户却无从回答）。
const props = defineProps<{ m: ChatMsg }>()

const store = useChatStore()
const draft = ref('')
const sending = ref(false)

async function submit() {
  const text = draft.value.trim()
  if (!text || sending.value || !props.m.askId) return
  sending.value = true
  try {
    await store.resolveAsk(props.m.askId, text)
    draft.value = ''
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <div class="flex max-w-full flex-col items-start gap-1">
    <div class="text-xs text-[var(--c-text-dim)]"><span class="font-medium">问答</span></div>
    <div class="w-full max-w-[90%] rounded-2xl border border-[var(--c-primary)] bg-[var(--c-primary-soft)] p-3">
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <AppIcon name="message" :size="14" class="shrink-0 text-[var(--c-primary)]" />
        <span class="font-medium text-[var(--c-text)]">AI 需要你确认</span>
        <span v-if="m.answered" class="stat px-2 py-0.5 text-xs text-[var(--c-ok-text)]">已回复</span>
      </div>
      <p class="mt-2 whitespace-pre-wrap text-sm leading-6 text-[var(--c-text)]">{{ m.question }}</p>
      <!-- 待答：有选项 → 点选即答；无选项 → 自由输入（回车发送，必须有出口） -->
      <div v-if="!m.answered && m.options?.length" class="mt-2.5 flex flex-wrap gap-2">
        <button v-for="opt in m.options" :key="opt" class="chip" @click="store.resolveAsk(m.askId!, opt)">
          {{ opt }}
        </button>
      </div>
      <form v-else-if="!m.answered" class="mt-2.5 flex items-center gap-2" @submit.prevent="submit">
        <input
          v-model="draft"
          class="min-w-0 flex-1 rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface)] px-3 py-1.5 text-sm text-[var(--c-text)] outline-none focus:border-[var(--c-primary)]"
          placeholder="输入你的回答，回车发送"
          :disabled="sending"
        />
        <button type="submit" class="btn-primary px-3 py-1.5 text-xs" :disabled="sending || !draft.trim()">
          {{ sending ? '发送中…' : '回复' }}
        </button>
      </form>
      <div
        v-else
        class="mt-2 rounded-lg bg-[var(--c-surface)] px-2.5 py-1.5 text-xs leading-5 text-[var(--c-text-dim)]"
      >
        你的回复：{{ m.content }}
      </div>
    </div>
  </div>
</template>
