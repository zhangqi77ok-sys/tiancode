<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useChatStore } from '../stores/chat'
import { useWorkspaceStore } from '../stores/workspace'
import { shortDir } from '../composables/workspaceLabel'
import { registerEsc } from '../composables/useEsc'
import AppIcon from './AppIcon.vue'

// 命令面板（Ctrl+K，0.0.19）：跳会话 / 进工作区 / 开设置 / 开面板，一个入口。
// 数据源全部是 store 里现成的事实（summaries / 最近工作区 / 当前模型），零新后端。
// 键盘：↑↓ 选择、Enter 执行、Esc 关闭——结果为空时 Enter 无操作。
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'open-channels'): void
  (e: 'open-mcp'): void
  (e: 'open-skills'): void
  (e: 'open-tones'): void
  (e: 'open-workspace-settings'): void
}>()

const store = useChatStore()
const ws = useWorkspaceStore()

interface Cmd {
  id: string
  icon: 'message' | 'folder' | 'sliders' | 'plug' | 'book' | 'plus' | 'command' | 'wrench'
  label: string
  hint: string
  run: () => void
}

const query = ref('')
const input = ref<HTMLInputElement | null>(null)
const cursor = ref(0)
// 面板是 v-if 挂载即打开的浮层：直接注册进 Esc 消费栈（卸载兜底注销）
const offEsc = registerEsc(() => emit('close'))
onBeforeUnmount(() => offEsc())

function jumpSession(id: string) {
  void store.selectSession(id)
  emit('close')
}
async function enterWorkspace(dir: string) {
  if (dir !== ws.path) await ws.setPath(dir)
  await store.newSession() // 与顶栏/侧栏同一语义：切空间 = 回到草稿开新对话
  emit('close')
}

const cmds = computed<Cmd[]>(() => {
  const q = query.value.trim().toLowerCase()
  const out: Cmd[] = []
  const push = (c: Cmd) => {
    if (!q || c.label.toLowerCase().includes(q) || c.hint.toLowerCase().includes(q)) out.push(c)
  }
  // 会话（最近活跃在前）
  const sessions = [...store.summaries].sort((a, b) => (b.lastActiveMs ?? 0) - (a.lastActiveMs ?? 0))
  for (const sm of sessions.slice(0, 30)) {
    push({
      id: `session:${sm.id}`,
      icon: 'message',
      label: sm.title || sm.id,
      hint: sm.workspace ? shortDir(sm.workspace) : '未分组',
      run: () => jumpSession(sm.id),
    })
  }
  // 工作区
  const wsSet = new Set<string>()
  if (ws.path) wsSet.add(ws.path)
  for (const sm of store.summaries) if (sm.workspace) wsSet.add(sm.workspace)
  for (const w of wsSet) {
    push({ id: `ws:${w}`, icon: 'folder', label: shortDir(w), hint: w, run: () => void enterWorkspace(w) })
  }
  // 动作
  push({
    id: 'action:new',
    icon: 'plus',
    label: '新建对话',
    hint: '草稿',
    run: () => {
      void store.newSession()
      emit('close')
    },
  })
  push({ id: 'action:channels', icon: 'sliders', label: '模型与渠道管理', hint: '设置', run: () => emit('open-channels') })
  push({ id: 'action:mcp', icon: 'plug', label: 'MCP 管理', hint: '设置', run: () => emit('open-mcp') })
  push({ id: 'action:skills', icon: 'book', label: 'Skill 管理', hint: '设置', run: () => emit('open-skills') })
  push({ id: 'action:tones', icon: 'message', label: '语气设置', hint: '设置', run: () => emit('open-tones') })
  push({ id: 'action:wssettings', icon: 'wrench', label: '工作区设置', hint: '设置', run: () => emit('open-workspace-settings') })
  return out
})

watch(query, () => {
  cursor.value = 0
})

watch(
  () => cmds.value.length,
  (n) => {
    if (cursor.value >= n) cursor.value = Math.max(0, n - 1)
  },
)

function move(delta: number) {
  if (!cmds.value.length) return
  cursor.value = (cursor.value + delta + cmds.value.length) % cmds.value.length
  void nextTick(scrollActiveIntoView)
}
function runActive() {
  cmds.value[cursor.value]?.run()
}
const listRef = ref<HTMLElement | null>(null)
function scrollActiveIntoView() {
  listRef.value
    ?.querySelector('[data-active="true"]')
    ?.scrollIntoView({ block: 'nearest' })
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    move(1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    move(-1)
  } else if (e.key === 'Enter') {
    e.preventDefault()
    runActive()
  }
}

onMounted(() => {
  input.value?.focus()
})
</script>

<template>
  <!-- 顶置浮层：点遮罩关闭；不抢 Esc 消费栈（useEscClose 已注册） -->
  <div class="fixed inset-0 z-50 flex items-start justify-center bg-black/30 pt-[12vh]" @click.self="emit('close')">
    <div
      class="w-[560px] max-w-[90vw] overflow-hidden rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] shadow-[var(--shadow-float)]"
      role="dialog"
      aria-label="命令面板"
      @keydown="onKeydown"
    >
      <div class="flex items-center gap-2 border-b border-[var(--c-border)] px-3 py-2.5">
        <AppIcon name="command" :size="14" class="shrink-0 text-[var(--c-text-faint)]" />
        <input
          ref="input"
          v-model="query"
          type="text"
          placeholder="跳会话 / 进工作区 / 打开设置…"
          aria-label="搜索命令"
          class="min-w-0 flex-1 bg-transparent text-sm text-[var(--c-text)] outline-none"
        />
        <span class="shrink-0 text-[10px] text-[var(--c-text-faint)]">↑↓ 选择 · Enter 执行 · Esc 关闭</span>
      </div>
      <div ref="listRef" class="max-h-[50vh] overflow-y-auto p-1.5">
        <p v-if="!cmds.length" class="px-3 py-6 text-center text-xs text-[var(--c-text-faint)]">没有匹配项</p>
        <button
          v-for="(c, i) in cmds"
          :key="c.id"
          :data-active="i === cursor"
          class="flex w-full items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm transition-colors"
          :class="i === cursor ? 'bg-[var(--c-primary-soft)] text-[var(--c-primary)]' : 'text-[var(--c-text-dim)] hover:bg-[var(--c-surface-soft)]'"
          :aria-selected="i === cursor"
          role="option"
          @mousemove="cursor = i"
          @click="c.run()"
        >
          <AppIcon :name="c.icon" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
          <span class="min-w-0 flex-1 truncate">{{ c.label }}</span>
          <span class="max-w-[40%] shrink-0 truncate text-[10px] text-[var(--c-text-faint)]" :title="c.hint">
            {{ c.hint }}
          </span>
        </button>
      </div>
    </div>
  </div>
</template>
