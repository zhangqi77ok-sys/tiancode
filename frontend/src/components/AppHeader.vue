<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useChatStore } from '../stores/chat'
import { useChannelStore } from '../stores/channels'
import { useWorkspaceStore } from '../stores/workspace'
import { useToast } from '../composables/useToast'
import { THEME_LABEL, currentTheme, cycleTheme, type ThemeMode } from '../composables/useTheme'
import { workspaceLabel } from '../composables/workspaceLabel'
import { bridge, winClose, winMinimize, winToggleMaximize } from '../wails'
import AppIcon from './AppIcon.vue'

// 顶栏：品牌 + 运行状态 + 导出/工作区/命令确认。
// 渠道/模型/技能/MCP 入口统一在侧栏底部（0.0.06）；汉堡导航已下线。
// 纯状态展示用 .stat（无 hover 态），可点操作用 .chip——不制造假可点。
defineProps<{ navOpen?: boolean }>()

const store = useChatStore()
const channels = useChannelStore()
const ws = useWorkspaceStore()
const { push: toast } = useToast()

const approvalOn = ref(false)

// 顶栏工作区（0.0.07）：主标签 = **这场对话正在用的路**（已落账会话取账本归属；
// 草稿取下一场的根），不再是笼统的"当前工作区"——ws.path 只影响还没落账的新对话，
// 把它当"当前"会误导用户以为正在看的对话会写进那个目录。
// 归属与下一场根不同时，副标签用弱样式另标「新建对话将使用 …」（禁止都叫"当前"）。
const isDraft = computed(() => !store.sessionId)
const sessionWs = computed(() => store.summaries.find((s) => s.id === store.sessionId)?.workspace ?? '')
const wsLabel = computed(() =>
  workspaceLabel({ isDraft: isDraft.value, sessionWorkspace: sessionWs.value, draftWorkspace: ws.path }),
)

// 状态灯文案：后台运行 / 待答复都要与"空闲"区分（切走后顶栏不能装作没事）。
// 0.0.06：带上会话标题——多个会话并行时"后台运行中"说不清是谁在跑。
const busyTitle = computed(() => (store.busyTarget ? store.titleOf(store.busyTarget) || '未命名会话' : ''))
const statusText = computed(() => {
  if (store.anyPending) return `${store.anyPending} 项待确认 · ${busyTitle.value}`
  if (!store.anyRunning) return '空闲'
  return store.running ? '运行中' : `后台运行中 · ${busyTitle.value}`
})

// 油表（0.0.09）：本轮 prompt token（上游 usage 的直接读数）。没有上下文长度
// 配置时只显示绝对数字——不编百分比。
const usageText = computed(() => {
  const n = store.promptTokens
  if (!n) return ''
  return n >= 10000 ? `上下文 ≈${(n / 1000).toFixed(1)}k tok` : `上下文 ${n} tok`
})

// 状态灯点击：跳到等待处理的会话（优先"待确认"，其次其他运行中会话）
function jumpToBusy() {
  if (store.busyTarget) void store.selectSession(store.busyTarget)
}

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

// 进入已有工作区：切换工具根并回到草稿。不改当前这场对话的工具——否则侧栏仍挂在旧空间，读写已经打到新目录。
// 0.0.06：生成中不再禁止切换——进行中的会话用自己的工具集（后端按会话持有根），
// 切换只影响"还没落账的新对话"。
async function enterWorkspace(dir: string) {
  wsMenuOpen.value = false
  if (dir === ws.path) return
  const ok = await ws.setPath(dir)
  if (ok) await store.newSession()
}

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

// ---- 导出（0.0.06 改版）----
// 生成中同样可用：导出走账本投影，已落账的部分不丢；运行中只导出"到目前为止"。
const exportMenuOpen = ref(false)
const exportMenuRef = ref<HTMLElement | null>(null)

async function currentMarkdown(): Promise<string | null> {
  if (!store.sessionId) return null
  const md = await store.exportMarkdown(store.sessionId)
  return md || null
}

// 复制 Markdown 到剪贴板；剪贴板失败时提示改用"另存为文件"（不假装成功）
async function exportCopy() {
  exportMenuOpen.value = false
  const md = await currentMarkdown()
  if (!md) return
  try {
    await navigator.clipboard.writeText(md)
    toast('info', '已复制为 Markdown')
  } catch {
    toast('error', '剪贴板不可用——可改用「另存为文件」')
  }
}

// 另存为文件：系统保存对话框走后端（WebView 内下载行为不可控）；剪贴板失败也有出路
async function exportSave() {
  exportMenuOpen.value = false
  const md = await currentMarkdown()
  if (!md) return
  try {
    await bridge().app.SaveTextFile('会话导出.md', md)
    toast('info', '已保存为 Markdown 文件')
  } catch (e) {
    toast('error', String(e instanceof Error ? e.message : e))
  }
}

function onExportMousedown(e: MouseEvent) {
  if (!exportMenuOpen.value) return
  const el = exportMenuRef.value
  if (el && !el.contains(e.target as Node)) exportMenuOpen.value = false
}

// 审批闸门开关（ADR-0007 默认关）：开启后 shell 命令执行前需你确认。
// 0.2.27 起同时覆盖 ext_manage（MCP/Skill 增删会让本机执行新命令——它与 shell
// 是同一类"执行面"，不纳入审批等于留了一条绕过确认的路径）
async function toggleApproval() {
  approvalOn.value = !approvalOn.value
  await store.setApprovalPolicy(approvalOn.value ? ['shell', 'ext_manage'] : [])
}

// ---- 主题切换（0.0.06）：跟随系统 → 浅色 → 深色 循环；偏好持久化 ----
const themeMode = ref<ThemeMode>(currentTheme())
function toggleTheme() {
  themeMode.value = cycleTheme()
}

onMounted(async () => {
  await channels.load()
  await ws.refresh()
  approvalOn.value = (await store.loadApprovalPolicy()).length > 0
  document.addEventListener('mousedown', onDocMousedown)
  document.addEventListener('mousedown', onExportMousedown)
})

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocMousedown)
  document.removeEventListener('mousedown', onExportMousedown)
})
</script>

<template>
  <!-- 无边框窗口的标题栏：整条可拖拽（交互元素在 CSS 里统一 no-drag） -->
  <header class="flex flex-wrap items-center justify-between gap-2" style="--wails-draggable: drag">
    <div class="flex min-w-0 items-center gap-2">
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
      <!-- 状态灯：后台会话在跑/待答复都要与"空闲"区分；有跳转目标时可点击直达 -->
      <button
        class="stat"
        :class="
          store.anyPending ? 'text-[var(--c-warn-text)]' : store.anyRunning ? 'text-[var(--c-primary)]' : ''
        "
        :disabled="!store.busyTarget"
        :title="store.busyTarget ? '点击跳到等待处理的会话' : ''"
        @click="jumpToBusy"
      >
        <span
          class="h-1.5 w-1.5 rounded-full"
          :class="
            store.anyPending
              ? 'bg-[var(--c-warn)]'
              : store.anyRunning
                ? 'animate-pulse bg-[var(--c-primary)]'
                : 'bg-[var(--c-text-faint)]'
          "
        ></span>
        {{ statusText }}
      </button>
      <!-- 油表（0.0.09）：有读数才显示；纯数据展示用 .stat -->
      <span v-if="usageText" class="stat">{{ usageText }}</span>
      <!-- 导出（0.0.06）：复制 / 另存文件二选一；生成中也可用（导出已落账部分） -->
      <div ref="exportMenuRef" class="relative">
        <button
          class="chip"
          :disabled="!store.sessionId"
          aria-haspopup="menu"
          :aria-expanded="exportMenuOpen"
          title="导出当前会话为 Markdown（生成中可导出已落账部分）"
          @click="exportMenuOpen = !exportMenuOpen"
        >
          <AppIcon name="download" :size="13" /> 导出
        </button>
        <div
          v-if="exportMenuOpen"
          role="menu"
          class="absolute right-0 top-full z-40 mt-1 w-56 rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] p-1.5 shadow-lg"
        >
          <button role="menuitem" class="menu-item" @click="exportCopy">
            <AppIcon name="copy" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="flex-1 text-left">复制 Markdown</span>
          </button>
          <button role="menuitem" class="menu-item" @click="exportSave">
            <AppIcon name="download" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="flex-1 text-left">另存为文件…</span>
          </button>
        </div>
      </div>
      <!-- 工作区（0.0.07）：主标签 = 这场对话正在用的路；弱样式副标签 = 新建对话的根。
           菜单内进入已有工作区 / 选新目录 / 退出纯对话 -->
      <div ref="wsMenuRef" class="relative flex items-center gap-1.5">
        <button
          class="chip"
          :class="wsLabel.main === '这场对话没有工作区' ? 'border-dashed text-[var(--c-text-dim)]' : ''"
          aria-haspopup="menu"
          :aria-expanded="wsMenuOpen"
          :title="wsLabel.mainTitle || '选择工作区'"
          @click="wsMenuOpen = !wsMenuOpen"
        >
          <AppIcon name="folder" :size="13" /> {{ wsLabel.main }}
        </button>
        <span
          v-if="wsLabel.sub"
          class="hidden text-[11px] text-[var(--c-text-faint)] lg:inline"
          :title="wsLabel.subTitle"
        >
          {{ wsLabel.sub }}
        </span>
        <div
          v-if="wsMenuOpen"
          role="menu"
          class="absolute right-0 top-full z-40 mt-1 w-80 rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] p-1.5 shadow-lg"
        >
          <div class="px-2 py-1 text-[11px] text-[var(--c-text-faint)]">工作区（点选后打开新对话，当前这场不会改目录）</div>
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

      <!-- 主题三态（0.0.06）：跟随系统 / 浅色 / 深色；颜色全走令牌重定义 -->
      <button
        class="chip"
        :title="`当前：${THEME_LABEL[themeMode]} · 点击切换`"
        aria-label="切换主题"
        @click="toggleTheme"
      >
        <AppIcon name="refresh" :size="13" /> 主题 {{ THEME_LABEL[themeMode] }}
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
