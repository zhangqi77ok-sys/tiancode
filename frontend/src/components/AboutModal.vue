<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { bridge } from '../wails'
import { useDialogs } from '../composables/useDialogs'
import { useToast } from '../composables/useToast'
import { errText } from '../composables/errText'
import BaseModal from './BaseModal.vue'
import AppIcon from './AppIcon.vue'

// 关于 tiancode 面板（0.0.45 任务 9）：
// 展示当前版本（AppVersion()），并提供「检查更新」按钮复用 CheckUpdate() 检查流程。
defineEmits<{ (e: 'close'): void }>()

const dialogs = useDialogs()
const { push: toast } = useToast()

const version = ref('')
const checking = ref(false)
const statusMsg = ref('')
const statusType = ref<'info' | 'error' | ''>('')

async function loadVersion() {
  try {
    const v = await bridge().app.AppVersion()
    version.value = v || 'dev'
  } catch {
    version.value = 'dev'
  }
}

onMounted(() => {
  void loadVersion()
})

async function checkUpdate() {
  if (checking.value) return
  checking.value = true
  statusMsg.value = ''
  statusType.value = ''

  try {
    const info = await bridge().app.CheckUpdate()
    if (!info) {
      statusMsg.value = '检查更新失败：后端未返回结果'
      statusType.value = 'error'
      toast('error', statusMsg.value)
      return
    }
    if (!info.hasUpdate) {
      const msg = `已是最新版本${info.current && info.current !== 'dev' ? `（v${info.current}）` : ''}`
      statusMsg.value = '已是最新版本'
      statusType.value = 'info'
      toast('info', msg)
      return
    }

    const sizeMB = info.assetSize ? `（约 ${(info.assetSize / 1024 / 1024).toFixed(1)} MB）` : ''
    const ok = await dialogs.confirm({
      title: '发现新版本',
      message: `当前 v${info.current} → 最新 v${info.latest}${sizeMB}

将下载安装包并自动安装，完成后应用会自动重启。
未保存的草稿已自动持久化，生成中的回合会被中断。`,
      confirmText: '立即更新',
    })
    if (!ok) return
    toast('info', '正在下载并安装，完成后应用将自动重启…')
    await bridge().app.ApplyUpdate()
  } catch (e) {
    statusMsg.value = errText(e)
    statusType.value = 'error'
    toast('error', statusMsg.value)
  } finally {
    checking.value = false
  }
}
</script>

<template>
  <BaseModal open title="关于 tiancode" panel-class="w-full max-w-[440px]" @close="$emit('close')">
    <div class="space-y-4 p-5">
      <div class="flex items-center gap-3">
        <div class="flex h-12 w-12 items-center justify-center rounded-xl bg-[var(--c-primary-soft)] text-[var(--c-primary)]">
          <AppIcon name="command" :size="24" />
        </div>
        <div>
          <h2 class="text-base font-semibold text-[var(--c-text)]">tiancode</h2>
          <p class="text-xs text-[var(--c-text-dim)]">版本：v{{ version }}</p>
        </div>
      </div>

      <p class="text-xs leading-relaxed text-[var(--c-text-dim)]">
        本地第一的 AI 代码助手。专注于极速交互、透明控制与可靠执行。
      </p>

      <div
        v-if="statusMsg"
        class="rounded-lg p-2.5 text-xs"
        :class="statusType === 'error' ? 'bg-[var(--c-err-soft)] text-[var(--c-err-text)]' : 'bg-[var(--c-surface-soft)] text-[var(--c-ok-text)]'"
      >
        {{ statusMsg }}
      </div>

      <div class="flex items-center justify-between border-t border-[var(--c-border)] pt-4">
        <span class="text-xs text-[var(--c-text-faint)]">软件更新</span>
        <button
          class="btn-primary px-3 py-1 text-xs"
          type="button"
          :disabled="checking"
          @click="checkUpdate"
        >
          {{ checking ? '正在检查…' : '检查更新' }}
        </button>
      </div>
    </div>
  </BaseModal>
</template>
