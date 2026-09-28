<script setup lang="ts">
import { computed, ref } from 'vue'
import { useChatStore } from '../stores/chat'
import { useChannelStore } from '../stores/channels'
import { useWorkspaceStore } from '../stores/workspace'
import { buildSidebar } from '../composables/sessionGrouping'
import { useDialogs } from '../composables/useDialogs'
import AppIcon from './AppIcon.vue'
import SessionRow from './SessionRow.vue'

// 会话侧栏（三段式，对齐商用 AI 工具）：置顶 / 会话（未归属空间）/ 空间（按工作区分组）。
// 双动作入口：新建对话（当前工作区）+ 打开工作区（切换后新对话归属该空间）。
// 每个列表默认显示 5 条，超出折叠为"查看更多 (N)"。
// 底部导航：渠道管理常驻入口（0.2.21）——入口从"顶栏 chip 专属"提升为导航级可见。
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'open-channels'): void
}>()

const store = useChatStore()
const channels = useChannelStore()
const ws = useWorkspaceStore()
const dialogs = useDialogs()

// 侧栏底部渠道摘要：当前激活渠道名（未配置时给出警示色引导）
const channelSide = computed(() => channels.activeModel || channels.activeChannel?.name || '')

const VIEW_LIMIT = 5

// 分区模型（纯函数 + 单测）：sections = 置顶 / 会话 / 空间；只镜像账本，草稿会话不可见
const sections = computed(() => buildSidebar(store.summaries, ws.path))

// 折叠状态：分区头与空间分组头共用（当前空间默认展开，其余默认收起；点击后以手动为准）
const collapsed = ref<Record<string, boolean>>({})
function toggle(key: string) {
  collapsed.value[key] = !collapsed.value[key]
}
function isOpen(key: string, defaultOpen: boolean): boolean {
  return collapsed.value[key] ?? defaultOpen
}

// 查看更多：每个列表独立展开态
const expanded = ref<Record<string, boolean>>({})
function visible<T extends { id: string }>(items: T[], key: string): T[] {
  return expanded.value[key] ? items : items.slice(0, VIEW_LIMIT)
}
// 查看更多点击：切换 expanded（0.2.10 曾误绑 toggle 折叠态——改的是另一张表，点击永远无效）
function toggleMore(key: string) {
  expanded.value[key] = !expanded.value[key]
}

// 打开工作区：系统目录选择框 → 切换 → 回到草稿态（等于在该空间开新对话）。
// 归属由首条消息落账本时的快照决定，天然记到新空间名下；草稿不进侧栏，反复切换不堆积空会话
async function openWorkspace() {
  if (store.running) return
  const ok = await ws.pickAndSet()
  if (ok) {
    await store.newSession()
    emit('close') // 窄屏抽屉收起；桌面端无副作用
  }
}

// 在已有工作区新建会话（空间组头 ＋）：直接切入（无目录选择器）→ 草稿。
// 这是"灵活选择已有工作区新建会话"的最短路径；运行中禁止（切工作区会重建工具集）
async function newInWorkspace(dir: string) {
  if (store.running) return
  await ws.setPath(dir)
  await store.newSession()
  emit('close')
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
    class="card flex w-60 shrink-0 flex-col p-3"
  >
    <!-- 双动作入口：新建对话（当前工作区）+ 打开工作区（切换归属） -->
    <div class="mb-3 flex gap-1.5">
      <button
        class="btn-primary min-w-0 flex-1 gap-1.5 py-2 text-sm"
        :title="ws.path ? `在当前工作区（${ws.path}）新建对话` : '新建通用对话（未选择工作区）'"
        @click="store.newSession(); emit('close')"
      >
        <AppIcon name="plus" :size="14" /> 新建对话
      </button>
      <button
        class="chip shrink-0 gap-1.5 px-3"
        title="打开工作区（切换并自动新建对话）"
        @click="openWorkspace"
      >
        <AppIcon name="folder" :size="14" /> 打开
      </button>
    </div>

    <div class="min-h-0 flex-1 space-y-2 overflow-y-auto">
      <template v-for="sec in sections" :key="sec.kind + sec.label">
        <!-- 置顶 / 会话：单列表 -->
        <div v-if="sec.kind !== 'spaces'" class="mb-1">
          <div class="flex items-center gap-1.5 px-2 py-1 text-[11px] font-medium text-[var(--c-text-faint)]">
            <AppIcon :name="sec.kind === 'pinned' ? 'star' : 'message'" :size="11" />
            {{ sec.label }} ({{ sec.items.length }})
          </div>
          <div class="space-y-0.5">
            <SessionRow
              v-for="sm in visible(sec.items, sec.kind)"
              :key="sm.id"
              :id="sm.id"
              :title="sm.title || sm.id"
              :last-active-ms="sm.lastActiveMs"
              :pinned="sm.pinned"
              :running="store.running && store.sessionId === sm.id"
              @select="select(sm.id)"
              @pin="(p) => store.pinSession(sm.id, p)"
              @rename="rename(sm.id)"
              @remove="remove(sm.id)"
            />
          </div>
          <button
            v-if="sec.items.length > VIEW_LIMIT"
            class="w-full rounded-lg px-2 py-1.5 text-left text-[11px] text-[var(--c-primary)] transition-colors hover:bg-[var(--c-primary-soft)]"
            @click="toggleMore(sec.kind)"
          >
            {{ expanded[sec.kind] ? '收起' : `查看更多 (${sec.items.length - VIEW_LIMIT})` }}
          </button>
        </div>

        <!-- 空间：按工作区的嵌套分组 -->
        <div v-else class="mb-1">
          <div class="flex items-center gap-1.5 px-2 py-1 text-[11px] font-medium text-[var(--c-text-faint)]">
            <AppIcon name="folder" :size="11" /> {{ sec.label }} ({{ sec.groups.length }})
          </div>
          <div v-for="g in sec.groups" :key="g.label" class="mb-1">
            <!-- 组头：折叠按钮 + 在该工作区新建（hover/focus 显现，不挤占常态视觉） -->
            <div
              class="group flex items-center gap-1 rounded-lg px-2 py-1.5 text-[11px] text-[var(--c-text-dim)] transition-colors hover:bg-[var(--c-surface-soft)]"
            >
              <button
                class="flex min-w-0 flex-1 items-center gap-1.5 text-left"
                :aria-expanded="isOpen('fold:' + g.label, g.isCurrent)"
                @click="toggle('fold:' + g.label)"
              >
                <AppIcon
                  name="chevron-down"
                  :size="10"
                  class="shrink-0 transition-transform"
                  :class="isOpen('fold:' + g.label, g.isCurrent) ? '' : '-rotate-90'"
                />
                <AppIcon
                  name="folder"
                  :size="11"
                  class="shrink-0"
                  :class="g.isCurrent ? 'text-[var(--c-primary)]' : ''"
                />
                <span
                  class="min-w-0 flex-1 truncate"
                  :class="g.isCurrent ? 'font-medium text-[var(--c-primary)]' : ''"
                >
                  {{ g.label }}
                </span>
                <span class="shrink-0 text-[10px] text-[var(--c-text-faint)]">{{ g.items.length }}</span>
              </button>
              <button
                class="shrink-0 rounded p-0.5 text-[var(--c-text-faint)] opacity-0 transition-opacity hover:text-[var(--c-primary)] focus-visible:opacity-100 group-hover:opacity-100"
                :title="`在 ${g.label} 新建对话`"
                :aria-label="`在 ${g.label} 新建对话`"
                :disabled="store.running"
                @click="newInWorkspace(g.workspace)"
              >
                <AppIcon name="plus" :size="12" />
              </button>
            </div>

            <div v-if="isOpen('fold:' + g.label, g.isCurrent)" class="mt-0.5 space-y-0.5">
              <SessionRow
                v-for="sm in visible(g.items, 'more:' + g.label)"
                :key="sm.id"
                :id="sm.id"
                :title="sm.title || sm.id"
                :last-active-ms="sm.lastActiveMs"
                :pinned="sm.pinned"
                :running="store.running && store.sessionId === sm.id"
                @select="select(sm.id)"
                @pin="(p) => store.pinSession(sm.id, p)"
                @rename="rename(sm.id)"
                @remove="remove(sm.id)"
              />
            </div>
            <!-- 查看更多与文件夹折叠用不同 key，互不打架 -->
            <button
              v-if="g.items.length > VIEW_LIMIT && isOpen('fold:' + g.label, g.isCurrent)"
              class="w-full rounded-lg px-2 py-1.5 text-left text-[11px] text-[var(--c-primary)] transition-colors hover:bg-[var(--c-primary-soft)]"
              @click="toggleMore('more:' + g.label)"
            >
              {{ expanded['more:' + g.label] ? '收起' : `查看更多 (${g.items.length - VIEW_LIMIT})` }}
            </button>
          </div>
        </div>
      </template>
    </div>

    <div v-if="store.error" class="mt-2 px-1 text-xs text-[var(--c-err-text)]">{{ store.error }}</div>

    <!-- 底部导航：渠道管理常驻入口（当前渠道名一眼可见；未配置给警示色） -->
    <div class="mt-2 border-t border-[var(--c-border)] px-1 pt-2">
      <button
        class="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-xs transition-colors hover:bg-[var(--c-surface-soft)]"
        aria-haspopup="dialog"
        title="模型渠道管理"
        @click="emit('open-channels')"
      >
        <AppIcon name="sliders" :size="13" class="shrink-0 text-[var(--c-text-dim)]" />
        <span class="shrink-0 text-[var(--c-text-dim)]">渠道管理</span>
        <span
          class="min-w-0 flex-1 truncate text-right"
          :class="channelSide ? 'text-[var(--c-text-faint)]' : 'text-[var(--c-warn-text)]'"
          :title="channels.activeChannel ? `${channels.activeChannel.name} · ${channels.activeModel || '未填模型'}` : '未配置渠道'"
        >
          {{ channelSide || '未配置' }}
        </span>
      </button>
    </div>
  </aside>
</template>
