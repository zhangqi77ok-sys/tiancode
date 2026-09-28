<script setup lang="ts">
import { useChatStore } from '../stores/chat'
import { useDialogs } from '../composables/useDialogs'
import AppIcon from './AppIcon.vue'

// 会话列表：桌面端常驻侧栏；窄屏（<md）为抽屉，由 App 控制开合。
// 行内操作常驻可见（55% 透明度）而非 hover 才出现——键盘/触屏用户也必须够得着。
const props = defineProps<{ open?: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const store = useChatStore()
const dialogs = useDialogs()

// 重命名：对话框返回 null 视为放弃；60 字上限与后端契约一致
async function rename(id: string) {
  if (store.running) return
  const next = await dialogs.prompt({
    title: '重命名会话',
    message: '会话标题（最多 60 字）',
    value: store.titleOf(id),
    maxlength: 60,
  })
  if (next === null) return
  await store.renameSession(id, next)
}

// 删除：不可逆操作，先确认（消息删除后无法从界面找回）
async function remove(id: string) {
  if (store.running) return
  const ok = await dialogs.confirm({
    title: '删除会话',
    message: `删除会话「${store.titleOf(id)}」？\n该会话的全部消息将被移除，且无法恢复。`,
    confirmText: '删除',
    danger: true,
  })
  if (ok) await store.removeSession(id)
}

function select(id: string) {
  void store.selectSession(id)
  emit('close') // 窄屏选中后收起抽屉；桌面端该事件无副作用
}
</script>

<template>
  <aside
    aria-label="会话列表"
    class="card flex w-60 shrink-0 flex-col p-3 max-md:fixed max-md:inset-y-0 max-md:left-0 max-md:z-[var(--z-overlay)] max-md:w-72 max-md:rounded-l-none max-md:rounded-r-2xl max-md:shadow-2xl max-md:transition-transform max-md:duration-200"
    :class="open ? 'max-md:translate-x-0' : 'max-md:-translate-x-full max-md:invisible'"
  >
    <button class="btn-primary mb-3 w-full gap-2 py-2 text-sm" @click="store.newSession()">
      <AppIcon name="plus" :size="14" /> 新建对话
    </button>

    <div class="min-h-0 flex-1 space-y-1 overflow-y-auto">
      <div v-for="id in store.sessions" :key="id" class="group flex items-center gap-1">
        <button
          class="min-w-0 flex-1 truncate rounded-xl px-3 py-2 text-left text-sm transition-colors"
          :class="
            id === store.sessionId
              ? 'bg-[var(--c-primary-soft)] font-medium text-[var(--c-primary)]'
              : 'text-[var(--c-text-dim)] hover:bg-[var(--c-surface-soft)]'
          "
          @click="select(id)"
        >
          {{ store.titleOf(id) }}
        </button>
        <button
          class="btn-ghost shrink-0"
          :disabled="store.running"
          title="重命名会话"
          aria-label="重命名会话"
          @click="rename(id)"
        >
          <AppIcon name="pencil" :size="14" />
        </button>
        <button
          class="btn-ghost shrink-0 hover:text-[var(--c-err-text)]"
          :disabled="store.running"
          title="删除会话"
          aria-label="删除会话"
          @click="remove(id)"
        >
          <AppIcon name="trash" :size="14" />
        </button>
      </div>
    </div>

    <div v-if="store.error" class="mt-2 px-1 text-xs text-[var(--c-err-text)]">{{ store.error }}</div>
    <div v-else class="mt-2 px-1 text-xs text-[var(--c-text-faint)]">历史由事件账本恢复</div>
  </aside>
</template>
