<script setup lang="ts">
import type { ChatMsg } from '../stores/chat'
import { useChatStore } from '../stores/chat'
import AppIcon from './AppIcon.vue'

defineProps<{ m: ChatMsg }>()

// 答复走 store：失败必须可见（error 位），成功后卡片转为已决态防重复点击
const store = useChatStore()
</script>

<template>
  <div class="flex max-w-full flex-col items-start gap-1">
    <div class="text-xs text-[var(--c-text-dim)]"><span class="font-medium">需要确认</span></div>
    <div class="w-full max-w-[90%] rounded-2xl border border-[var(--c-warn)] bg-[var(--c-warn-soft)] p-3">
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <AppIcon name="shield" :size="14" class="text-[var(--c-warn-text)]" />
        <span class="font-medium">即将执行工具</span>
        <span class="stat px-2 py-0.5 text-xs">{{ m.toolName }}</span>
        <!-- 会话名（0.2.36 审计 R3）：多会话下用户必须一眼知道这条命令是哪个对话要跑的 -->
        <span v-if="m.sessionTitle" class="stat px-2 py-0.5 text-xs text-[var(--c-text-dim)]" title="发起该请求的会话">
          会话：{{ m.sessionTitle }}
        </span>
        <!-- 已决状态用 -text 色（AA 达标），不再是旧版的浅色字 -->
        <span v-if="m.status === 'approved'" class="stat px-2 py-0.5 text-xs text-[var(--c-ok-text)]">已允许</span>
        <span v-else-if="m.status === 'denied'" class="stat px-2 py-0.5 text-xs text-[var(--c-err-text)]">已拒绝</span>
      </div>
      <!-- 原始参数原样展示：用户必须看到确切要执行什么（ADR-0007） -->
      <pre class="mt-2 max-h-40 overflow-auto whitespace-pre-wrap rounded-lg bg-[var(--c-surface)] px-2 py-1.5 font-mono text-xs leading-5">{{ m.args }}</pre>
      <div v-if="!m.status" class="mt-2 flex gap-2">
        <button class="btn-primary px-4 py-1.5 text-xs" @click="store.resolveApproval(m.approvalId!, true)">
          允许执行
        </button>
        <button class="chip" @click="store.resolveApproval(m.approvalId!, false, '用户拒绝')">拒绝</button>
      </div>
    </div>
  </div>
</template>
