<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useCatalogStore } from '../stores/catalog'
import { useChannelStore } from '../stores/channels'
import AppIcon from './AppIcon.vue'

// 左侧滑出的导航栏。会话列表不在这里——会话栏保持原位。
const props = defineProps<{ open?: boolean }>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'open-channels'): void
  (e: 'open-mcp'): void
  (e: 'open-skills'): void
}>()

const channels = useChannelStore()
const catalog = useCatalogStore()
const activeModel = computed(() => channels.activeModel)
const activeName = computed(() => channels.activeChannel?.name ?? '')
const mcpOn = computed(() => catalog.mcp.filter((s) => s.enabled).length)
const skillOn = computed(() => catalog.skills.filter((s) => s.enabled).length)

const root = ref<HTMLElement | null>(null)

// 打开时焦点进入导航：键盘用户开面板后 Tab 应从第一项开始，
// 而不是继续在背后（被 inert 排除后无从进面板）
watch(
  () => props.open,
  async (open) => {
    if (!open) return
    await nextTick()
    root.value?.querySelector<HTMLElement>('nav button')?.focus()
  },
)
</script>

<template>
  <aside
    id="app-nav"
    ref="root"
    aria-label="导航"
    :inert="props.open ? undefined : true"
    :aria-hidden="!props.open"
    class="fixed inset-y-0 left-0 z-[var(--z-overlay)] flex w-64 flex-col border-r border-[var(--c-border)] bg-[var(--c-surface)] shadow-2xl transition-transform duration-200"
    :class="props.open ? 'translate-x-0' : 'pointer-events-none -translate-x-full'"
  >
    <div class="flex items-center justify-between border-b border-[var(--c-border)] px-4 py-3">
      <span class="text-sm font-semibold">导航</span>
      <button class="btn-ghost" aria-label="关闭导航" @click="emit('close')">
        <AppIcon name="x" :size="16" />
      </button>
    </div>
    <nav class="flex flex-col gap-1 p-2">
      <button class="menu-item" @click="emit('open-channels')">
        <AppIcon name="sliders" :size="16" class="shrink-0 text-[var(--c-text-dim)]" />
        <span class="min-w-0 flex-1">渠道管理</span>
        <span
          class="max-w-[7rem] truncate text-right text-[11px]"
          :class="activeModel || activeName ? 'text-[var(--c-text-faint)]' : 'text-[var(--c-warn-text)]'"
        >
          {{ activeModel || activeName || '未配置' }}
        </span>
      </button>
      <button class="menu-item" @click="emit('open-mcp')">
        <AppIcon name="plug" :size="16" class="shrink-0 text-[var(--c-text-dim)]" />
        <span class="min-w-0 flex-1">MCP 管理</span>
        <span class="text-[11px] text-[var(--c-text-faint)]">
          {{ catalog.mcp.length ? `已启用 ${mcpOn}/${catalog.mcp.length}` : '未添加' }}
        </span>
      </button>
      <button class="menu-item" @click="emit('open-skills')">
        <AppIcon name="book" :size="16" class="shrink-0 text-[var(--c-text-dim)]" />
        <span class="min-w-0 flex-1">Skill 管理</span>
        <span class="text-[11px] text-[var(--c-text-faint)]">
          {{ catalog.skills.length ? `已启用 ${skillOn}/${catalog.skills.length}` : '未添加' }}
        </span>
      </button>
    </nav>
  </aside>
</template>

