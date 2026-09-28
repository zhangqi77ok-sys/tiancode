<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useChatStore } from '../stores/chat'
import { useChannelStore } from '../stores/channels'
import { useWorkspaceStore } from '../stores/workspace'
import { useToast } from '../composables/useToast'
import AppIcon from './AppIcon.vue'
import ChannelSettings from './ChannelSettings.vue'

// 顶栏：品牌 + 运行状态 + 导出/工作区/命令确认/渠道设置。
// 纯状态展示用 .stat（无 hover 态），可点操作用 .chip——不制造假可点。
const emit = defineEmits<{ (e: 'toggle-drawer'): void }>()

const store = useChatStore()
const channels = useChannelStore()
const ws = useWorkspaceStore()
const { push: toast } = useToast()

const settingsOpen = ref(false)
const approvalOn = ref(false)

// 顶栏只显示末级目录名（完整路径太长会挤掉状态区）；状态在 workspace store（侧栏分组同源）
const workspaceName = computed(
  () => ws.path.split(/[\\/]/).filter(Boolean).pop() ?? '未设置工作区',
)

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
})
</script>

<template>
  <header class="flex flex-wrap items-center justify-between gap-2">
    <div class="flex min-w-0 items-baseline gap-2">
      <button class="btn-ghost md:hidden" aria-label="会话列表" @click="emit('toggle-drawer')">
        <AppIcon name="menu" :size="18" />
      </button>
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
      <button class="chip" title="切换工作区" @click="switchWorkspace">
        <AppIcon name="folder" :size="13" /> {{ workspaceName }}
      </button>
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
        title="模型渠道设置"
        @click="settingsOpen = true"
      >
        <AppIcon name="sliders" :size="13" /> {{ hasChannel ? activeChannelName : '未配置渠道 · 点击设置' }}
      </button>
    </div>

    <ChannelSettings v-if="settingsOpen" @close="settingsOpen = false" />
  </header>
</template>
