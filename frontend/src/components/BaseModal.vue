<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'

// 通用模态骨架：Teleport + 遮罩 + Esc 关闭 + 焦点陷阱。
// 焦点陷阱是可达性硬要求：Tab 必须循环在面板内，关闭后焦点不能落到遮罩背后的内容上。
const props = defineProps<{
  open: boolean
  title?: string
  /** 面板宽度类（如 'w-[720px]'），缺省为窄面板 */
  panelClass?: string
}>()
const emit = defineEmits<{ (e: 'close'): void }>()

const panel = ref<HTMLElement | null>(null)

// Tab 循环：Shift+Tab 在首个元素上回绕到最后，Tab 在最后元素上回绕到首个
function trapTab(e: KeyboardEvent) {
  if (e.key !== 'Tab' || !panel.value) return
  const focusables = panel.value.querySelectorAll<HTMLElement>(
    'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
  )
  if (!focusables.length) return
  const first = focusables[0]
  const last = focusables[focusables.length - 1]
  if (e.shiftKey && document.activeElement === first) {
    e.preventDefault()
    last.focus()
  } else if (!e.shiftKey && document.activeElement === last) {
    e.preventDefault()
    first.focus()
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.stopPropagation()
    emit('close')
  }
  trapTab(e)
}

// 捕获阶段监听：优先于面板内部控件的 Esc 处理（如有）。
// immediate 必须有：面板常以 v-if + open（恒定 true）挂载，watch 默认首帧不触发——
// 少了它会 Esc 关不掉、焦点陷阱也从未注册（0.2.26 实机：渠道/MCP/技能三个面板都中招）。
watch(
  () => props.open,
  async (open) => {
    if (open) {
      document.addEventListener('keydown', onKeydown, true)
      await nextTick()
      panel.value?.focus()
    } else {
      document.removeEventListener('keydown', onKeydown, true)
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown, true))
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="fixed inset-0 z-[var(--z-modal)] flex items-center justify-center bg-[var(--c-overlay)] p-6"
      @click.self="emit('close')"
    >
      <div
        ref="panel"
        tabindex="-1"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
        class="card flex max-h-[85vh] flex-col overflow-hidden outline-none"
        :class="panelClass || 'w-full max-w-md'"
      >
        <header
          v-if="title"
          class="flex items-center justify-between border-b border-[var(--c-border)] px-5 py-4"
        >
          <h2 class="text-base font-semibold">{{ title }}</h2>
          <button class="btn-ghost" aria-label="关闭" @click="emit('close')">
            <AppIcon name="x" :size="16" />
          </button>
        </header>
        <slot />
      </div>
    </div>
  </Teleport>
</template>
