<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import { registerEsc } from '../composables/useEsc'
import AppIcon from './AppIcon.vue'

// 快捷键速查浮层（? 呼出，0.0.19）：快捷键无处可查就等于没有——
// 一张卡列全全部全局键，Esc 或点遮罩关闭。
const emit = defineEmits<{ (e: 'close'): void }>()
const offEsc = registerEsc(() => emit('close'))
onBeforeUnmount(() => offEsc())

const GROUPS: { title: string; items: [string, string][] }[] = [
  {
    title: '会话',
    items: [
      ['Ctrl+N', '新建对话（草稿）'],
      ['Ctrl+↑ / Ctrl+↓', '上一条 / 下一条会话'],
      ['Ctrl+B', '收起 / 展开侧栏'],
      ['Ctrl+K', '命令面板：跳会话 / 进工作区 / 开设置'],
      ['?', '本速查'],
    ],
  },
  {
    title: '输入与生成',
    items: [
      ['Ctrl+I 或 Ctrl+/', '聚焦输入框'],
      ['Esc', '关浮层；没有浮层时中断正在进行的生成'],
    ],
  },
  {
    title: '当前会话',
    items: [
      ['Ctrl+F', '会话内搜索（消息正文）'],
      ['Ctrl+Shift+F', '搜索所有会话（跨会话）'],
    ],
  },
]
onMounted(() => {
  // 挂载即打开：焦点给遮罩以便 Esc 生效（Esc 走消费栈，无需焦点也可）。
})
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/30" @click.self="emit('close')">
    <div
      class="w-[480px] max-w-[90vw] rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] p-4 shadow-[var(--shadow-float)]"
      role="dialog"
      aria-label="快捷键速查"
    >
      <div class="mb-3 flex items-center gap-2">
        <AppIcon name="command" :size="14" class="shrink-0 text-[var(--c-text-faint)]" />
        <h2 class="text-sm font-medium text-[var(--c-text)]">快捷键</h2>
        <button class="btn-ghost ml-auto" aria-label="关闭" @click="emit('close')">
          <AppIcon name="x" :size="14" />
        </button>
      </div>
      <div v-for="g in GROUPS" :key="g.title" class="mb-3 last:mb-0">
        <p class="mb-1 text-[11px] font-medium text-[var(--c-text-faint)]">{{ g.title }}</p>
        <div v-for="[k, desc] in g.items" :key="k" class="flex items-center gap-3 py-1 text-xs">
          <kbd
            class="min-w-[110px] shrink-0 rounded-md border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-2 py-0.5 text-center text-[11px] text-[var(--c-text-dim)]"
          >
            {{ k }}
          </kbd>
          <span class="text-[var(--c-text-dim)]">{{ desc }}</span>
        </div>
      </div>
    </div>
  </div>
</template>
