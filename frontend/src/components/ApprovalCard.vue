<script setup lang="ts">
import { computed } from 'vue'
import type { ChatMsg } from '../stores/chat'
import { useChatStore } from '../stores/chat'
import { useWorkspaceStore } from '../stores/workspace'
import AppIcon from './AppIcon.vue'

// 审批卡（0.0.06 改版）：主内容是人能读的句子——工具名、工作目录、命令或文件路径、
// 是否像删除/覆盖；原始 JSON 折进「详情」。用户批准前要的是"要干什么"，不是读 JSON。
const props = defineProps<{ m: ChatMsg }>()

// 答复走 store：失败必须可见（error 位），成功后卡片转为已决态防重复点击
const store = useChatStore()
const ws = useWorkspaceStore()

// 解析原始参数（解析失败静默——详情里仍有原文可看）
const parsed = computed<Record<string, unknown>>(() => {
  try {
    return JSON.parse(props.m.args || '{}') as Record<string, unknown>
  } catch {
    return {}
  }
})

// 一句话摘要：按工具选主参数（shell=命令，fs=read/write/replace/list/tree=path，
// git=action+path，search=pattern，ext_manage=action+name）
const summaryText = computed(() => {
  const a = parsed.value
  const s = (v: unknown) => (typeof v === 'string' ? v.trim() : '')
  switch (props.m.toolName) {
    case 'shell': {
      const cmd = s(a.command)
      return cmd ? `执行命令：${cmd}` : '执行命令'
    }
    case 'fs': {
      const path = s(a.path) || '(未指定)'
      const act = s(a.action)
      if (act === 'write') return `写入文件：${path}`
      if (act === 'replace') return `修改文件：${path}`
      return `访问文件：${path}`
    }
    case 'git':
      return `git ${s(a.action) || 'status'}${s(a.path) ? `（${s(a.path)}）` : ''}`
    case 'search':
      return `搜索：${s((a as { pattern?: unknown }).pattern) || '(未指定)'}`
    case 'ext_manage':
      return `扩展管理：${s(a.action) || ''} ${s(a.name) || ''}`.trim()
    default:
      return `调用工具 ${props.m.toolName}`
  }
})

// 危险信号（0.0.06）：命令或路径像"删除/覆盖"时必须显式说——不是猜意图，是关键词事实
const dangerNote = computed(() => {
  const a = parsed.value
  const cmd = typeof a.command === 'string' ? a.command : ''
  const act = typeof a.action === 'string' ? a.action : ''
  const deleteish = /\b(rm|rmdir|del|rd|erase|format|Remove-Item|Clear-Content|truncate)\b|\/s\b|\/f\b|-rf\b/i.test(cmd)
  const overwriteish = (props.m.toolName === 'fs' && (act === 'write' || act === 'replace'))
  if (deleteish) return '这条命令可能删除或覆盖文件'
  // 0.0.11：写/改文件不再说"不可自动撤销"——单次 write/replace 有写入前快照
  //（卡片上的「恢复写入前」/ 轮次撤回都能还原）；删除类命令的警示依旧保留
  if (overwriteish) return '这将修改磁盘上的文件'
  return ''
})

// ext_manage 也算"执行面"（0.2.27）：写明它会改本机扩展配置
const extNote = computed(() => (props.m.toolName === 'ext_manage' ? '该操作会添加或移除本机的 MCP/Skill 扩展' : ''))
</script>

<template>
  <div class="flex max-w-full flex-col items-start gap-1">
    <div class="text-xs text-[var(--c-text-dim)]"><span class="font-medium">需要确认</span></div>
    <div class="w-full max-w-[90%] rounded-2xl border border-[var(--c-warn)] bg-[var(--c-warn-soft)] p-3">
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <AppIcon name="shield" :size="14" class="text-[var(--c-warn-text)]" />
        <span class="font-medium">即将执行工具</span>
        <span class="stat px-2 py-0.5 text-xs">{{ m.toolName }}</span>
        <!-- 会话名（0.2.36 审计 R3）：多会话下用户必须一眼知道这条命令是哪个对话要跑的 -->
        <span v-if="m.sessionTitle" class="stat px-2 py-0.5 text-xs text-[var(--c-text-dim)]" title="发起该请求的会话">
          会话：{{ m.sessionTitle }}
        </span>
        <!-- 已决状态用 -text 色（AA 达标），不再是旧版的浅色字 -->
        <span v-if="m.status === 'approved'" class="stat px-2 py-0.5 text-xs text-[var(--c-ok-text)]">已允许</span>
        <span v-else-if="m.status === 'denied'" class="stat px-2 py-0.5 text-xs text-[var(--c-err-text)]">已拒绝</span>
      </div>

      <!-- 人话主内容：要干什么 + 在哪个目录 + 危险信号 -->
      <p class="mt-2 break-all text-sm leading-6 text-[var(--c-text)]">
        {{ summaryText }}
      </p>
      <p class="mt-1 text-xs text-[var(--c-text-dim)]">
        工作目录：{{ ws.path || '（纯对话，无工作区）' }}
      </p>
      <p v-if="dangerNote" class="mt-1 flex items-center gap-1 text-xs font-medium text-[var(--c-warn-text)]">
        <AppIcon name="alert" :size="12" /> {{ dangerNote }}
      </p>
      <p v-if="extNote" class="mt-1 text-xs text-[var(--c-warn-text)]">{{ extNote }}</p>

      <!-- 原始参数折进详情：确切内容仍可查（ADR-0007 不变），但不再是主内容 -->
      <details class="mt-2">
        <summary class="cursor-pointer select-none text-xs text-[var(--c-text-dim)] hover:text-[var(--c-text)]">
          详情（原始参数）
        </summary>
        <pre class="mt-1 max-h-40 overflow-auto whitespace-pre-wrap rounded-lg bg-[var(--c-surface)] px-2 py-1.5 font-mono text-xs leading-5">{{ m.args }}</pre>
      </details>

      <div v-if="!m.status" class="mt-2 flex gap-2">
        <button class="btn-primary px-4 py-1.5 text-xs" @click="store.resolveApproval(m.approvalId!, true)">
          允许执行
        </button>
        <button class="chip" @click="store.resolveApproval(m.approvalId!, false, '用户拒绝')">拒绝</button>
      </div>
    </div>
  </div>
</template>
