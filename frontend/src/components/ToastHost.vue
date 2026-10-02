<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import { useToast } from '../composables/useToast'

// 全局通知宿主：底部居中、role=status + aria-live=polite（读屏可达）。
// 刻意不做堆叠复杂动效——通知的职责是"被看见"，不是"被欣赏"。
// 带动作的 toast 主体可点（0.0.21：后台审批/问答 toast 点击跳转会话）。
const { toasts, activate, dismiss } = useToast()
</script>

<template>
  <div
    class="pointer-events-none fixed inset-x-0 bottom-6 z-[var(--z-toast)] flex flex-col items-center gap-2 px-4"
    role="status"
    aria-live="polite"
  >
    <TransitionGroup
      enter-active-class="transition duration-200"
      enter-from-class="translate-y-2 opacity-0"
      leave-active-class="transition duration-150"
      leave-to-class="opacity-0"
    >
      <div
        v-for="t in toasts"
        :key="t.id"
        class="pointer-events-auto flex items-center gap-2 rounded-full border px-4 py-2 text-sm shadow-[var(--shadow-float)]"
        :class="[
          t.kind === 'error'
            ? 'border-[var(--c-err)] bg-[var(--c-err-soft)] text-[var(--c-err-text)]'
            : 'border-[var(--c-border)] bg-[var(--c-surface)]',
          t.action ? 'cursor-pointer transition-transform hover:scale-[1.02]' : '',
        ]"
        :role="t.action ? 'button' : undefined"
        :tabindex="t.action ? 0 : undefined"
        @click="activate(t)"
        @keydown.enter="activate(t)"
      >
        <span>{{ t.text }}</span>
        <!-- Tailwind v4 无前缀 important 语法，紧凑尺寸直接用工具类组合 -->
        <button
          class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[var(--c-text-dim)] transition-colors hover:text-[var(--c-text)]"
          aria-label="关闭通知"
          @click.stop="dismiss(t.id)"
        >
          <AppIcon name="x" :size="14" />
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>
