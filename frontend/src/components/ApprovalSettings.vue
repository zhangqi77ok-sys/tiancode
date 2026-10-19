<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { bridge } from '../wails'
import { useChatStore } from '../stores/chat'
import { useDialogs } from '../composables/useDialogs'
import { errText } from '../composables/errText'
import BaseModal from './BaseModal.vue'

// 审批与权限设置面板（0.0.45 任务 5）：
// 上半部分配置全局审批工具清单（由后端 ApprovableTools 单一来源驱动）；
// 下半部分列出当前会话内「总是允许」的 shell 前缀并支持即时撤销。
defineEmits<{ (e: 'close'): void }>()

const store = useChatStore()
const dialogs = useDialogs()

const approvableTools = ref<string[]>([])
const selectedTools = ref<string[]>([])
const sessionPrefixes = ref<string[]>([])

const loading = ref(true)
const saving = ref(false)
const error = ref('')
const saved = ref(false)

const TOOL_DESCRIPTIONS: Record<string, { label: string; desc: string }> = {
  shell: { label: '执行命令', desc: '在终端中运行 shell 命令行' },
  browser: { label: '操作浏览器（可提交表单）', desc: '控制浏览器导航、点击与表单提交' },
  mcp: { label: '调用 MCP 工具', desc: '执行外部 MCP 插件提供的工具' },
  ext_manage: { label: '增删扩展', desc: '安装、卸载或修改扩展插件' },
  fs: { label: '读写文件', desc: '读取、修改或删除工作区文件' },
}

function toolLabel(name: string): string {
  return TOOL_DESCRIPTIONS[name]?.label ?? name
}

function toolDesc(name: string): string {
  return TOOL_DESCRIPTIONS[name]?.desc ?? ''
}

async function loadData() {
  loading.value = true
  error.value = ''
  try {
    const [allTools, currentPolicy] = await Promise.all([
      bridge().app.ApprovableTools(),
      bridge().app.ApprovalPolicy(),
    ])
    approvableTools.value = allTools ?? []
    selectedTools.value = [...(currentPolicy ?? [])]

    if (store.sessionId) {
      const allows = await bridge().app.SessionShellAllow(store.sessionId)
      sessionPrefixes.value = allows ?? []
    } else {
      sessionPrefixes.value = []
    }
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void loadData()
})

function toggleTool(name: string) {
  saved.value = false
  if (selectedTools.value.includes(name)) {
    selectedTools.value = selectedTools.value.filter((t) => t !== name)
  } else {
    selectedTools.value.push(name)
  }
}

async function restoreDefault() {
  saved.value = false
  error.value = ''
  try {
    const defaults = await store.loadDefaultApprovalPolicy()
    selectedTools.value = [...defaults]
  } catch (e) {
    error.value = errText(e)
  }
}

async function save() {
  error.value = ''
  saved.value = false

  // 全部取消勾选保存 = 关闭审批，保存前用确认框提示"将关闭全部审批"
  if (selectedTools.value.length === 0) {
    const ok = await dialogs.confirm({
      title: '确认关闭审批',
      message: '将关闭全部审批',
      confirmText: '确定关闭',
      danger: true,
    })
    if (!ok) return
  }

  // 顺序以 ApprovableTools() 为准
  const toolsToSave = approvableTools.value.filter((t) => selectedTools.value.includes(t))

  saving.value = true
  try {
    const ok = await store.setApprovalPolicy(toolsToSave)
    if (!ok) {
      error.value = store.error || '保存审批策略失败'
      return
    }
    saved.value = true
  } catch (e) {
    error.value = errText(e)
  } finally {
    saving.value = false
  }
}

async function revokePrefix(prefix: string) {
  error.value = ''
  if (!store.sessionId) return
  try {
    await bridge().app.RevokeSessionShellAllow(store.sessionId, prefix)
    // 撤销成功后刷新前缀列表
    const allows = await bridge().app.SessionShellAllow(store.sessionId)
    sessionPrefixes.value = allows ?? []
  } catch (e) {
    error.value = errText(e)
  }
}
</script>

<template>
  <BaseModal open title="审批与权限" panel-class="w-full max-w-[640px]" @close="$emit('close')">
    <div class="space-y-5 p-5">
      <p v-if="error" class="text-xs text-[var(--c-err-text)]">{{ error }}</p>
      <p v-else-if="saved" class="text-xs text-[var(--c-ok-text)]">审批设置已保存生效。</p>

      <!-- 上半部分：执行前需要确认的工具 -->
      <section class="space-y-3">
        <div class="flex items-center justify-between">
          <div>
            <h3 class="text-xs font-medium text-[var(--c-text)]">执行前需要确认的工具</h3>
            <p class="text-[11px] text-[var(--c-text-faint)]">勾选的工具在调用前需弹出确认卡片，未勾选的直接执行</p>
          </div>
          <div class="flex items-center gap-2">
            <button
              class="chip text-xs"
              type="button"
              :disabled="saving"
              @click="restoreDefault"
            >
              恢复默认
            </button>
            <button
              class="btn-primary px-3 py-1 text-xs"
              type="button"
              :disabled="saving"
              @click="save"
            >
              {{ saving ? '保存中…' : '保存' }}
            </button>
          </div>
        </div>

        <div v-if="loading" class="py-3 text-center text-xs text-[var(--c-text-faint)]">正在载入…</div>
        <div v-else class="space-y-2">
          <label
            v-for="tool in approvableTools"
            :key="tool"
            class="flex cursor-pointer items-start gap-2.5 rounded-lg border border-[var(--c-border)] p-2.5 transition-colors hover:bg-[var(--c-surface-soft)]"
          >
            <input
              type="checkbox"
              class="mt-0.5 rounded border-[var(--c-border)] text-[var(--c-primary)]"
              :value="tool"
              :checked="selectedTools.includes(tool)"
              @change="toggleTool(tool)"
            />
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-1.5">
                <span class="text-xs font-medium text-[var(--c-text)]">{{ toolLabel(tool) }}</span>
                <span class="font-mono text-[11px] text-[var(--c-text-faint)]">({{ tool }})</span>
              </div>
              <p v-if="toolDesc(tool)" class="mt-0.5 text-[11px] text-[var(--c-text-dim)]">
                {{ toolDesc(tool) }}
              </p>
            </div>
          </label>
        </div>
      </section>

      <div class="h-px bg-[var(--c-border)]"></div>

      <!-- 下半部分：本会话已允许的命令前缀 -->
      <section class="space-y-2.5">
        <div>
          <h3 class="text-xs font-medium text-[var(--c-text)]">本会话已允许的命令前缀</h3>
          <p class="text-[11px] text-[var(--c-text-faint)]">在审批卡片中点击「总是允许同前缀」授予的前缀命令；重启应用后失效</p>
        </div>

        <div v-if="sessionPrefixes.length" class="space-y-1.5">
          <div
            v-for="p in sessionPrefixes"
            :key="p"
            class="flex items-center justify-between rounded-lg border border-[var(--c-border)] px-3 py-2 text-xs"
          >
            <code class="font-mono text-[var(--c-text)]">{{ p }}</code>
            <button
              class="chip text-[11px] text-[var(--c-err-text)] hover:bg-[var(--c-err-soft)]"
              type="button"
              title="撤销此前缀的免确认权限"
              @click="revokePrefix(p)"
            >
              撤销
            </button>
          </div>
        </div>
        <p v-else class="py-2 text-xs text-[var(--c-text-dim)]">
          本会话还没有授予过
        </p>
      </section>
    </div>
  </BaseModal>
</template>
