<script setup lang="ts">
import { computed, ref } from 'vue'
import { useChatStore } from '../stores/chat'
import { useWorkspaceStore } from '../stores/workspace'
import { groupSessions } from '../composables/sessionGrouping'
import { useDialogs } from '../composables/useDialogs'
import AppIcon from './AppIcon.vue'

// 会话侧栏：双动作入口（新建对话 / 打开工作区）+ 按空间分组
// （参考商用 AI 工具的"空间"侧栏：当前空间排最前且默认展开，其余折叠）。
// 行内操作常驻可见（55% 透明度）——键盘/触屏用户也必须够得着。
const props = defineProps<{ open?: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const store = useChatStore()
const ws = useWorkspaceStore()
const dialogs = useDialogs()

// 分组（纯函数 + 单测）：当前空间排最前；新建未发送的会话归当前空间最上方
const groups = computed(() => groupSessions(store.summaries, store.sessions, ws.path))

// 折叠态：当前空间默认展开，其余默认收起；用户点击后以手动为准
const collapsed = ref<Record<string, boolean>>({})
function toggle(label: string) {
  collapsed.value[label] = !collapsed.value[label]
}
function isOpen(label: string, isCurrent: boolean): boolean {
  return collapsed.value[label] ?? isCurrent
}

// 打开工作区：系统目录选择框 → 切换（新对话将归属该空间）
async function openWorkspace() {
  const ok = await ws.pickAndSet()
  if (ok) emit('close') // 窄屏抽屉收起；桌面端无副作用
}

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
    <!-- 双动作入口：新建对话 + 打开工作区（新对话将归属该空间） -->
    <div class="mb-3 flex gap-1.5">
      <button
        class="btn-primary min-w-0 flex-1 gap-1.5 py-2 text-sm"
        title="在当前工作区新建对话"
        @click="store.newSession(); emit('close')"
      >
        <AppIcon name="plus" :size="14" /> 新建对话
      </button>
      <button
        class="chip shrink-0 gap-1.5 px-3"
        title="打开工作区（切换后新建的对话将归属该空间）"
        @click="openWorkspace"
      >
        <AppIcon name="folder" :size="14" /> 打开
      </button>
    </div>

    <div class="min-h-0 flex-1 space-y-1 overflow-y-auto">
      <div v-for="g in groups" :key="g.label" class="mb-1">
        <!-- 分组头：空间名 + 数量；当前空间高亮 -->
        <button
          class="flex w-full items-center gap-1.5 rounded-lg px-2 py-1.5 text-left text-[11px] text-[var(--c-text-dim)] transition-colors hover:bg-[var(--c-surface-soft)]"
          :aria-expanded="isOpen(g.label, g.isCurrent)"
          @click="toggle(g.label)"
        >
          <AppIcon
            name="chevron-down"
            :size="10"
            class="shrink-0 transition-transform"
            :class="isOpen(g.label, g.isCurrent) ? '' : '-rotate-90'"
          />
          <AppIcon name="folder" :size="11" class="shrink-0" :class="g.isCurrent ? 'text-[var(--c-primary)]' : ''" />
          <span
            class="min-w-0 flex-1 truncate"
            :class="g.isCurrent ? 'font-medium text-[var(--c-primary)]' : ''"
          >
            {{ g.label }}
          </span>
          <span class="shrink-0 text-[10px] text-[var(--c-text-faint)]">{{ g.items.length }}</span>
        </button>

        <div v-if="isOpen(g.label, g.isCurrent)" class="mt-0.5 space-y-0.5">
          <div v-for="sItem in g.items" :key="sItem.id" class="group flex items-center gap-1">
            <button
              class="min-w-0 flex-1 truncate rounded-xl px-3 py-2 text-left text-sm transition-colors"
              :class="
                sItem.id === store.sessionId
                  ? 'bg-[var(--c-primary-soft)] font-medium text-[var(--c-primary)]'
                  : 'text-[var(--c-text-dim)] hover:bg-[var(--c-surface-soft)]'
              "
              @click="select(sItem.id)"
            >
              {{ sItem.title || sItem.id }}
            </button>
            <button
              class="btn-ghost shrink-0"
              :disabled="store.running"
              title="重命名会话"
              aria-label="重命名会话"
              @click="rename(sItem.id)"
            >
              <AppIcon name="pencil" :size="14" />
            </button>
            <button
              class="btn-ghost shrink-0 hover:text-[var(--c-err-text)]"
              :disabled="store.running"
              title="删除会话"
              aria-label="删除会话"
              @click="remove(sItem.id)"
            >
              <AppIcon name="trash" :size="14" />
            </button>
          </div>
        </div>
      </div>
    </div>

    <div v-if="store.error" class="mt-2 px-1 text-xs text-[var(--c-err-text)]">{{ store.error }}</div>
    <div v-else class="mt-2 px-1 text-xs text-[var(--c-text-faint)]">历史由事件账本恢复</div>
  </aside>
</template>
