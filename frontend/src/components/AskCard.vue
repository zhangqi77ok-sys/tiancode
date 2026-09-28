<script setup lang="ts">
import type { ChatMsg } from '../stores/chat'
import { useChatStore } from '../stores/chat'
import AppIcon from './AppIcon.vue'

// 问答卡（ask_user）：待答态展示问题 + 选项按钮；已答态展示所选答案。
// 答复走 store：失败必须可见（error 位），成功后卡片转已答态防重复点击。
defineProps<{ m: ChatMsg }>()

const store = useChatStore()
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
      <!-- 待答：选项按钮（点选即答复回流）；无选项 = 自由回答场景由模型收尾，不给输入 -->
      <div v-if="!m.answered && m.options?.length" class="mt-2.5 flex flex-wrap gap-2">
        <button v-for="opt in m.options" :key="opt" class="chip" @click="store.resolveAsk(m.askId!, opt)">
          {{ opt }}
        </button>
      </div>
      <div
        v-else-if="m.answered"
        class="mt-2 rounded-lg bg-[var(--c-surface)] px-2.5 py-1.5 text-xs leading-5 text-[var(--c-text-dim)]"
      >
        你的回复：{{ m.content }}
      </div>
    </div>
  </div>
</template>
