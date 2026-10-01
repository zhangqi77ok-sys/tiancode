<script setup lang="ts">
import { computed, provide, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useChatStore, type PendingAttachment, type TodoItem } from './stores/chat'
import type { ChatToolEventDTO, CheckResultDTO } from './wails'
import { useCatalogStore } from './stores/catalog'
import { loadSidebarCollapsed, matchShortcut, saveSidebarCollapsed } from './composables/shortcuts'
import { consumeEsc } from './composables/useEsc'
import { useToast } from './composables/useToast'
import { bridge } from './wails'
import AppHeader from './components/AppHeader.vue'
import AppIcon from './components/AppIcon.vue'
import BrowserPanel from './components/BrowserPanel.vue'
import ChannelSettings from './components/ChannelSettings.vue'
import CheckResults from './components/CheckResults.vue'
import Composer from './components/Composer.vue'
import DialogHost from './components/DialogHost.vue'
import FileDetailPanel from './components/FileDetailPanel.vue'
import FileTreePanel from './components/FileTreePanel.vue'
import FloatingTodo from './components/FloatingTodo.vue'
import McpSettings from './components/McpSettings.vue'
import MessageList from './components/MessageList.vue'
import RightPanel, { type RightPanelTabDef } from './components/RightPanel.vue'
import SessionList from './components/SessionList.vue'
import SkillSettings from './components/SkillSettings.vue'
import TasksPanel from './components/TasksPanel.vue'
import ToastHost from './components/ToastHost.vue'
import ToneSettings from './components/ToneSettings.vue'
import TurnReview from './components/TurnReview.vue'
import WorkspaceSettingsPanel from './components/WorkspaceSettingsPanel.vue'

// 根组件退化为布局壳：顶栏/侧栏/对话/输入各自自治，事件桥在此统一接线。
const store = useChatStore()
const { push: toast } = useToast()

// 会话操作的失败原来只写在侧栏最底部，容易被挡住——通知先冒出来。
// toast 后清空 error：否则同一文案连续出现时 watch 不再触发（第二次静默无声）。
watch(
  () => store.error,
  (msg) => {
    if (!msg) return
    toast('error', msg)
    store.error = ''
  },
)

const channelsOpen = ref(false)
const mcpOpen = ref(false)
const skillsOpen = ref(false)
const tonesOpen = ref(false) // 语气设置（第 8 批）：侧栏底部入口，不进顶栏
const wsSettingsOpen = ref(false) // 工作区设置（第 8 批）：在这一行打开 / 检查命令
// 工作区检查结果（第 8 批）：最近一次自动检查的位置列表（只读展示）
const checkResult = ref<CheckResultDTO | null>(null)

// 右栏 tab 注册表（0.0.28）：开着的 tab 才进注册表，全关时面板整体退场。
// 扩展方式（契约详注见 RightPanel.vue）：这里加一项 + 模板加同名插槽 `#tab-<id>`，
// 容器零改动——「目录」「任务」照此办理。
const rightTabs = computed<RightPanelTabDef[]>(() => {
  const tabs: RightPanelTabDef[] = []
  if (store.fileDetailPath) {
    tabs.push({
      id: 'file',
      label: '文件',
      icon: 'file',
      active: store.rightPanelTab === 'file',
      activate: () => {
        store.rightPanelTab = 'file'
      },
      close: () => store.closeFileDetail(),
    })
  }
  if (store.browserOpen) {
    tabs.push({
      id: 'browser',
      label: '浏览器',
      icon: 'image',
      active: store.rightPanelTab === 'browser',
      activate: () => {
        store.rightPanelTab = 'browser'
      },
      close: () => store.closeBrowserPanel(),
    })
  }
  if (store.treeOpen) {
    tabs.push({
      id: 'tree',
      label: '目录',
      icon: 'folder',
      active: store.rightPanelTab === 'tree',
      activate: () => {
        store.rightPanelTab = 'tree'
      },
      close: () => store.closeTreePanel(),
    })
  }
  if (store.tasksOpen) {
    tabs.push({
      id: 'tasks',
      label: '任务',
      icon: 'terminal',
      active: store.rightPanelTab === 'tasks',
      activate: () => {
        store.rightPanelTab = 'tasks'
      },
      close: () => store.closeTasksPanel(),
    })
  }
  return tabs
})
// 右栏全关时的竖标入口（0.3）：目录/任务两个"手动开栏" tab。
// 文件/浏览器由对话动作自动带出，不在这里。
const railTabs = [
  { label: '目录', icon: 'folder' as const, open: () => store.openTreePanel() },
  { label: '任务', icon: 'terminal' as const, open: () => store.openTasksPanel() },
]

// 模态守卫收敛一处：新增模态只需在这里登记（此前用三个布尔枚举，新增必漏）
const anyModalOpen = computed(
  () => channelsOpen.value || mcpOpen.value || skillsOpen.value || tonesOpen.value || wsSettingsOpen.value,
)

// 输入草稿**按会话各存一份**：此前是全局单例——在 A 里敲的半句话切到 B
// 回车就发进了 B（串会话），A 的草稿也随之丢失。切换/新建时草稿各归各位。
const drafts = ref<Record<string, string>>({})
const draft = computed({
  get: () => drafts.value[store.sessionId] ?? '',
  set: (v: string) => {
    drafts.value[store.sessionId] = v
  },
})

// 重跑锚点（第 7 批）：用户气泡点「重跑」只做两件事——原文回填输入框（可改）、
// 附件回到待发送区。撤回与账本分叉等用户按发送、确认之后才发生（见 Composer）。
const pendingRerun = ref<{ seq: number; attachments: PendingAttachment[] } | null>(null)
function startRerun(p: { seq: number; text: string; attachments: PendingAttachment[] }) {
  draft.value = p.text
  pendingRerun.value = { seq: p.seq, attachments: p.attachments }
}

// 切换/新建会话时关闭文件详情面板（面板展示的是"当前会话的改动"，跨会话显示会张冠李戴）；
// 待重跑锚点同理作废——草稿按会话各存一份，锚点不能跨会话用
watch(
  () => store.sessionId,
  () => {
    store.closeFileDetail()
    pendingRerun.value = null
    checkResult.value = null // 检查结果按会话各归各位（跨会话显示会张冠李戴）
  },
)

// 模态打开函数（0.0.06：入口统一在侧栏底部，汉堡导航已下线）
function openChannels() {
  channelsOpen.value = true
}
function openMcp() {
  mcpOpen.value = true
}
function openSkills() {
  skillsOpen.value = true
}
function openTones() {
  tonesOpen.value = true
}

// ---- 快捷键（0.0.06）----
//   Ctrl/Cmd+N 新建对话；Ctrl/Cmd+I 或 Ctrl/Cmd+/ 聚焦输入框；
//   Ctrl/Cmd+↑/↓ 上一条/下一条会话；Esc：模态打开时不抢（BaseModal 捕获层处理），
//   否则中断生成。全部在模态打开时停用（不与模态内输入抢键）。
const composerInput = ref<HTMLTextAreaElement | null>(null)
// Composer 注册它的 textarea（快捷键聚焦用）：函数注入，避免类型耦合
provide('registerComposerInput', (el: HTMLTextAreaElement | null) => {
  composerInput.value = el
})

function sessionSiblings(): string[] {
  // 侧栏顺序即会话顺序：按空间分组后的扁平 id 列表（含当前会话）
  const ids = store.summaries.map((s) => s.id)
  const inList = store.sessionId && ids.includes(store.sessionId)
  if (!inList && store.sessionId) return [store.sessionId, ...ids]
  return ids
}

function cycleSession(delta: number) {
  const ids = sessionSiblings()
  if (ids.length === 0) return
  const cur = ids.indexOf(store.sessionId)
  const next = cur < 0 ? (delta > 0 ? 0 : ids.length - 1) : (cur + delta + ids.length) % ids.length
  void store.selectSession(ids[next])
}

// 侧栏折叠（第 8 批）：Ctrl+B 收起成窄轨（文件详情占右侧时对话列不再被挤扁）。
// 状态记 localStorage（跨重启保留）；模态打开时不抢键——上面那行 return 已经兜住。
const sidebarCollapsed = ref(loadSidebarCollapsed())
function toggleSidebar() {
  sidebarCollapsed.value = !sidebarCollapsed.value
  saveSidebarCollapsed(sidebarCollapsed.value)
}

function onGlobalKeydown(e: KeyboardEvent) {
  if (anyModalOpen.value) return // 模态打开：不抢任何全局键（Esc 归模态）
  if (e.key === 'Escape') {
    // 浮层优先（第 3 批）：@ 候选等已 preventDefault；右侧文件面板/菜单/灯箱经
    // Esc 消费栈注册。没有浮层消费时才轮到"中断生成"——否则关一个浮层会顺手
    // 把正在跑的回合静默杀掉（coding 场景最恼火的一类误伤）。
    if (e.defaultPrevented || consumeEsc()) return
    if (store.running) {
      e.preventDefault()
      store.stop()
    }
    return
  }
  switch (matchShortcut(e)) {
    case 'new-session':
      e.preventDefault()
      void store.newSession()
      break
    case 'focus-input':
      e.preventDefault()
      composerInput.value?.focus()
      break
    case 'prev-session':
      e.preventDefault()
      cycleSession(-1)
      break
    case 'next-session':
      e.preventDefault()
      cycleSession(1)
      break
    case 'toggle-sidebar':
      e.preventDefault()
      toggleSidebar()
      break
    default:
      break
  }
}

onMounted(() => {
  window.addEventListener('keydown', onGlobalKeydown)
  // 先接线再 init：流式事件由 store 的会话守卫（sessionID 比对）兜住，不会串会话
  bridge().runtime.EventsOn('chat:chunk', (p: { sessionID: string; delta: string; thinking: string }) => {
    store.onChunk(p)
  })
  bridge().runtime.EventsOn('chat:terminal', (p: { sessionID: string; endReason: number; error: string }) => {
    store.onTerminal(p)
  })
  // 油表（0.0.09）：本轮 prompt token（上下文大小读数），顶栏显示
  bridge().runtime.EventsOn(
    'chat:usage',
    (p: { sessionID: string; prompt: number; completion: number; total: number }) => {
      store.onUsage(p)
    },
  )
  bridge().runtime.EventsOn('chat:tool', (p: ChatToolEventDTO) => {
    store.onTool(p)
  })
  // 审批卡片：sessionID 必填（后端必推）——缺标识的卡片宁可丢弃并报错，
  // 也绝不插进当前视图（那正是"数据串会话"）
  bridge().runtime.EventsOn(
    'chat:approval',
    (p: { id: string; sessionID: string; sessionTitle?: string; toolName: string; arguments: string }) => {
      store.onApproval(p)
    },
  )
  bridge().runtime.EventsOn('chat:todo', (p: { sessionID: string; items: { text: string; status: string }[] }) => {
    store.onTodo({ sessionID: p.sessionID, items: p.items as TodoItem[] })
  })
  // 工作区检查结果（第 8 批）：回合收尾后自动跑的命令，结果只给当前会话显示
  bridge().runtime.EventsOn('workspace:check', (p: CheckResultDTO) => {
    if (p?.sessionID && p.sessionID !== store.sessionId) return // 后台会话的结果不插进当前视图
    checkResult.value = p?.refs?.length ? p : null
  })
  // 上下文治理读数（第 2 批）：油表显示预算/估算与折叠标记——折叠绝不静默
  bridge().runtime.EventsOn(
    'chat:context',
    (p: {
      sessionID: string
      estimatedTokens: number
      budgetTokens: number
      foldedImages: number
      foldedTools: number
      foldedReads: number
      dropped: boolean
    }) => {
      store.onContext(p)
    },
  )
  // 问答卡：sessionID 必填（与审批同款，0.2.25 多会话）
  bridge().runtime.EventsOn(
    'chat:ask',
    (p: { id: string; sessionID: string; question: string; options?: string[] }) => {
      store.onAsk(p)
    },
  )
  void store.init()
  void useCatalogStore().load()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onGlobalKeydown)
})
</script>

<template>
  <!-- 贴边布局（阶段 1）：整页不再留 p-4 / gap-4 与浮卡——侧栏、对话、文件区各占一列，
       之间只有 1px 分隔线；圆角与阴影留给输入框、菜单、toast、模态。 -->
  <div class="flex h-screen flex-col">
    <AppHeader />

    <div class="flex min-h-0 flex-1">
      <SessionList
        :collapsed="sidebarCollapsed"
        @toggle-collapsed="toggleSidebar"
        @open-channels="openChannels"
        @open-mcp="openMcp"
        @open-skills="openSkills"
        @open-tones="openTones"
        @open-workspace-settings="wsSettingsOpen = true"
      />

      <!-- 对话列（0.3 工作台）：中列不再铺白底（body 渐变透出来），内容限宽居中——
           侧栏/右栏用表面色与本列区分，不再靠 1px 硬分割线。 -->
      <main class="relative flex min-w-0 flex-1">
        <!-- 左列：消息流 + 本轮变更 + 输入（与右侧面板并存，互不遮挡）。
             对话区保持可读最小宽度：右栏（文件/浏览器）打开时不把对话挤扁。 -->
        <div class="flex min-w-[320px] flex-1 flex-col">
          <div class="mx-auto flex min-h-0 w-full max-w-3xl flex-1 flex-col">
            <MessageList @suggest="draft = $event" @rerun="startRerun" />
            <!-- 本轮变更审查带（0.0.09；第 2 批默认折叠、按文件聚合）：点文件行开右侧详情 -->
            <TurnReview />
            <CheckResults :result="checkResult" />
            <Composer v-model="draft" :rerun="pendingRerun" @rerun-done="pendingRerun = null" />
          </div>
          <!-- 悬浮任务清单：挂在对话面板内（absolute 以 main 为参照系），位置/折叠态跨重启保留 -->
          <FloatingTodo />
        </div>
        <!-- 右栏容器（0.0.28 tab 化）：文件详情 / 浏览器驾驶舱 / 目录树 / 后台任务，
             Esc 经容器统一消费 -->
        <RightPanel v-if="rightTabs.length" :tabs="rightTabs">
          <template #tab-file>
            <FileDetailPanel />
          </template>
          <template #tab-browser>
            <BrowserPanel />
          </template>
          <template #tab-tree>
            <FileTreePanel />
          </template>
          <template #tab-tasks>
            <TasksPanel />
          </template>
        </RightPanel>
        <!-- 右栏全关时的常驻开栏轨（0.3 改版）：目录/任务是"想要才打开"的 tab（文件/浏览器由
             对话动作自动带出），没有入口这两个 tab 就永远到不了 tab 条上。
             图标下带文字——只认图形记不住"这是哪扇门"（纯图标轨的可发现性太差）。 -->
        <aside
          v-else
          class="flex w-16 shrink-0 flex-col items-center gap-1.5 bg-[var(--c-surface)] py-2"
          aria-label="打开右栏面板"
        >
          <button
            v-for="t in railTabs"
            :key="t.label"
            class="flex w-14 flex-col items-center gap-1 rounded-xl px-1 py-2 text-[11px] text-[var(--c-text-dim)] transition-colors hover:bg-[var(--c-surface-soft)] hover:text-[var(--c-primary)]"
            :aria-label="`打开${t.label}面板`"
            @click="t.open()"
          >
            <AppIcon :name="t.icon" :size="16" />
            {{ t.label }}
          </button>
        </aside>
      </main>
    </div>

    <!-- 全局宿主：渠道管理 + 对话框 + 通知（各挂一个） -->
    <ChannelSettings v-if="channelsOpen" @close="channelsOpen = false" />
    <McpSettings v-if="mcpOpen" @close="mcpOpen = false" />
    <SkillSettings v-if="skillsOpen" @close="skillsOpen = false" />
    <ToneSettings v-if="tonesOpen" @close="tonesOpen = false" />
    <WorkspaceSettingsPanel v-if="wsSettingsOpen" @close="wsSettingsOpen = false" />
    <DialogHost />
    <ToastHost />
  </div>
</template>
