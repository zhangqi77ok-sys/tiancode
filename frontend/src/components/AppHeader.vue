<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useChatStore } from '../stores/chat'
import { useChannelStore } from '../stores/channels'
import { useWorkspaceStore } from '../stores/workspace'
import { useToast } from '../composables/useToast'
import { winClose, winMinimize, winToggleMaximize } from '../wails'
import AppIcon from './AppIcon.vue'

// 顶栏：品牌 + 运行状态 + 导出/工作区/命令确认/渠道设置。
// 纯状态展示用 .stat（无 hover 态），可点操作用 .chip——不制造假可点。
const emit = defineEmits<{
  (e: 'toggle-drawer'): void
  (e: 'open-channels'): void
}>()

const store = useChatStore()
const channels = useChannelStore()
const ws = useWorkspaceStore()
const { push: toast } = useToast()

const approvalOn = ref(false)

// 顶栏只显示末级目录名（完整路径太长会挤掉状态区）；状态在 workspace store（侧栏分组同源）
const workspaceName = computed(
  () => ws.path.split(/[\\/]/).filter(Boolean).pop() ?? '选择工作区',
)

// ---- 工作区菜单（默认无工作区：进入/切换/退出都要显式、快速）----
const wsMenuOpen = ref(false)
const wsMenuRef = ref<HTMLElement | null>(null)

// 最近工作区：从会话摘要的归属去重（当前排最前）——不需要后端新接口，
// 侧栏空间分组的数据源即"用户用过哪些工作区"的事实
const recentWorkspaces = computed(() => {
  const set = new Set<string>()
  if (ws.path) set.add(ws.path)
  for (const s of store.summaries) if (s.workspace) set.add(s.workspace)
  return [...set]
})

function onDocMousedown(e: MouseEvent) {
  if (!wsMenuOpen.value) return
  const el = wsMenuRef.value
  if (el && !el.contains(e.target as Node)) wsMenuOpen.value = false
}

// 进入已有工作区（无目录选择器）：只切换不强制新建——用户可继续历史会话或点"新建对话"
async function enterWorkspace(dir: string) {
  wsMenuOpen.value = false
  await ws.setPath(dir)
}

// 顶栏展示当前默认渠道：没有渠道时给出明确引导（而不是让用户对着发送键发呆）
const activeChannelName = computed(
  () => channels.list.find((c) => c.active)?.name ?? '未配置渠道',
)
const hasChannel = computed(() => channels.list.length > 0 && !!channels.activeId)

// 切换工作区：弹系统目录选择框；状态收敛在 workspace store（侧栏"按空间分组"同源）。
// 与侧栏"打开"同语义：切换空间即回到草稿开新对话（归属由首条消息落账本时决定）
async function switchWorkspace() {
  const ok = await ws.pickAndSet()
  if (ok) await store.newSession()
}

// 选择其他目录并新建对话（菜单项）：语义同 switchWorkspace
async function pickWorkspace() {
  wsMenuOpen.value = false
  await switchWorkspace()
}

// 退出工作区：纯对话模式（本地文件工具下线），之后的会话无归属落"会话"区
async function exitWorkspace() {
  wsMenuOpen.value = false
  await ws.clear()
  await store.newSession()
}

// 导出当前会话为 Markdown 并复制到剪贴板；剪贴板不可用时明确报错，不假装成功
async function exportSession() {
  if (!store.sessionId || store.running) return
  const md = await store.exportMarkdown(store.sessionId)
  if (!md) return
  try {
    await navigator.clipboard.writeText(md)
    toast('info', '已导出并复制到剪贴板（Markdown）')
  } catch {
    toast('error', '导出失败：当前环境剪贴板不可用')
  }
}

// 审批闸门开关（ADR-0007 默认关）：开启后 shell 命令执行前需你确认
async function toggleApproval() {
  approvalOn.value = !approvalOn.value
  await store.setApprovalPolicy(approvalOn.value ? ['shell'] : [])
}

onMounted(async () => {
  await channels.load()
  await ws.refresh()
  approvalOn.value = (await store.loadApprovalPolicy()).length > 0
  document.addEventListener('mousedown', onDocMousedown)
})

onBeforeUnmount(() => document.removeEventListener('mousedown', onDocMousedown))
</script>

<template>
  <!-- 无边框窗口的标题栏：整条可拖拽（交互元素在 CSS 里统一 no-drag） -->
  <header class="flex flex-wrap items-center justify-between gap-2" style="--wails-draggable: drag">
    <div class="flex min-w-0 items-center gap-2">
      <button class="btn-ghost md:hidden" aria-label="会话列表" @click="emit('toggle-drawer')">
        <AppIcon name="menu" :size="18" />
      </button>
      <!-- 品牌 Logo：T 字标（内联，无外部资源） -->
      <span
        class="grid h-7 w-7 shrink-0 select-none place-items-center rounded-[10px] bg-[var(--c-primary)] text-[15px] font-bold text-white shadow-sm"
        aria-hidden="true"
        >T</span
      >
      <h1 class="text-[20px] font-semibold tracking-tight">tiancode</h1>
      <span class="hidden text-xs text-[var(--c-text-dim)] sm:inline">桌面 AI 编程智能体</span>
    </div>

    <div class="flex flex-wrap items-center gap-2">
      <span class="stat" :class="store.running ? 'text-[var(--c-primary)]' : ''">
        <span
          class="h-1.5 w-1.5 rounded-full"
          :class="store.running ? 'animate-pulse bg-[var(--c-primary)]' : 'bg-[var(--c-text-faint)]'"
        ></span>
        {{ store.running ? '运行中' : '空闲' }}
      </span>
      <button
        class="chip"
        :disabled="!store.sessionId || store.running"
        title="导出当前会话为 Markdown"
        @click="exportSession"
      >
        <AppIcon name="download" :size="13" /> 导出
      </button>
      <!-- 工作区：默认未选择；菜单内进入已有工作区 / 选新目录 / 退出纯对话 -->
      <div ref="wsMenuRef" class="relative">
        <button
          class="chip"
          :class="ws.path ? '' : 'border-dashed text-[var(--c-text-dim)]'"
          aria-haspopup="menu"
          :aria-expanded="wsMenuOpen"
          :title="ws.path ? `当前工作区：${ws.path}` : '未选择工作区（纯对话）· 点击进入'"
          @click="wsMenuOpen = !wsMenuOpen"
        >
          <AppIcon name="folder" :size="13" /> {{ workspaceName }}
        </button>
        <div
          v-if="wsMenuOpen"
          role="menu"
          class="absolute right-0 top-full z-40 mt-1 w-80 rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] p-1.5 shadow-lg"
        >
          <div class="px-2 py-1 text-[11px] text-[var(--c-text-faint)]">工作区（选中后新建的对话归属它）</div>
          <p v-if="!recentWorkspaces.length" class="px-2 py-1.5 text-xs text-[var(--c-text-dim)]">
            还没有工作区——选择目录后，新对话将归属它；不选则按通用会话处理
          </p>
          <button
            v-for="w in recentWorkspaces"
            :key="w"
            role="menuitem"
            class="menu-item"
            @click="enterWorkspace(w)"
          >
            <AppIcon name="folder" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="min-w-0 flex-1 truncate text-left">{{ w }}</span>
            <span v-if="w === ws.path" class="shrink-0 text-[10px] text-[var(--c-primary)]">当前</span>
          </button>
          <div class="my-1 h-px bg-[var(--c-border)]"></div>
          <button role="menuitem" class="menu-item" @click="pickWorkspace">
            <AppIcon name="folder" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="flex-1 text-left">选择其他目录并新建对话…</span>
          </button>
          <button
            v-if="ws.path"
            role="menuitem"
            class="menu-item text-[var(--c-err-text)]"
            @click="exitWorkspace"
          >
            <AppIcon name="x" :size="13" class="shrink-0" />
            <span class="flex-1 text-left">退出工作区（纯对话）</span>
          </button>
        </div>
      </div>
      <button
        class="chip"
        :class="approvalOn ? 'border-[var(--c-primary)] text-[var(--c-primary)]' : ''"
        :aria-pressed="approvalOn"
        title="开启后 shell 命令执行前需你确认"
        @click="toggleApproval"
      >
        <AppIcon name="shield" :size="13" /> 命令确认 {{ approvalOn ? '开' : '关' }}
      </button>
      <button
        class="chip"
        :class="hasChannel ? '' : 'border-[var(--c-warn)] text-[var(--c-warn-text)]'"
        aria-haspopup="dialog"
        title="模型渠道管理"
        @click="emit('open-channels')"
      >
        <AppIcon name="sliders" :size="13" /> {{ hasChannel ? activeChannelName : '未配置渠道 · 点击设置' }}
      </button>

      <!-- 窗口控制（无边框自绘）：最小化 / 最大化还原 / 关闭 -->
      <div class="ml-1 flex shrink-0 items-center gap-0.5">
        <button class="win-btn" aria-label="最小化" title="最小化" @click="winMinimize">
          <AppIcon name="minus" :size="14" />
        </button>
        <button class="win-btn" aria-label="最大化或还原" title="最大化 / 还原" @click="winToggleMaximize">
          <AppIcon name="stop" :size="11" />
        </button>
        <button class="win-btn win-btn-close" aria-label="关闭窗口" title="关闭" @click="winClose">
          <AppIcon name="x" :size="14" />
        </button>
      </div>
    </div>

  </header>
</template>
