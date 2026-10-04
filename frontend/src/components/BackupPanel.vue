<script setup lang="ts">
import { ref } from 'vue'
import BaseModal from './BaseModal.vue'
import { bridge, type BackupPreviewDTO } from '../wails'
import { useDialogs } from '../composables/useDialogs'
import { useToast } from '../composables/useToast'
import { errText } from '../composables/errText'

// 备份与恢复面板（0.0.23）：本地优先应用的数据是无价的——换机/重装/迁移不该
// 靠手工拷 %APPDATA%。导出即快照；导入先预检（给摘要）再确认，落地前自动存
// 一份 .bak（结果里写明在哪，想退回就有路）。

const emit = defineEmits<{ (e: 'close'): void }>()
const dialogs = useDialogs()
const { push: toast } = useToast()

const busy = ref(false)
const preview = ref<BackupPreviewDTO | null>(null)
const pickedPath = ref('')
const overwrite = ref(true)

// 版本串不再由前端持有（0.0.30 审查 R4）：此前这里写死了一个字面量（一度停在
// 0.0.23，仓库 VERSION 已是 0.0.29），导出后预检一直显示"来自 0.0.23"。版本是
// 后端的唯一真值（release.ps1 用 ldflags 注入），文件名与清单都由后端自己写。

async function doExport() {
  if (busy.value) return
  busy.value = true
  try {
    const path = await bridge().app.ExportBackupTo()
    if (!path) return // 用户取消了对话框
    toast('info', `已导出：${path}`)
  } catch (e) {
    toast('error', errText(e))
  } finally {
    busy.value = false
  }
}

async function doPick() {
  if (busy.value) return
  busy.value = true
  try {
    const path = await bridge().app.PickBackupFile()
    if (!path) return // 取消
    pickedPath.value = path
    preview.value = (await bridge().app.PreviewBackup(path)) ?? null
  } catch (e) {
    toast('error', errText(e))
  } finally {
    busy.value = false
  }
}

async function doImport() {
  if (!preview.value?.valid || busy.value) return
  const pv = preview.value
  const ok = await dialogs.confirm({
    title: '恢复数据',
    message: overwrite.value
      ? `将用备份覆盖同名数据（${pv.fileCount} 个文件 / ${pv.sessionCount} 场会话，来自 ${pv.version || '未知版本'}）。\n导入前会自动把当前数据另存一份 .bak 兜底。\n恢复后请重启应用。`
      : `只补缺失文件（不覆盖现有的）：${pv.fileCount} 个文件 / ${pv.sessionCount} 场会话。\n导入前会自动把当前数据另存一份 .bak 兜底。\n恢复后请重启应用。`,
    confirmText: '恢复',
    danger: true,
  })
  if (!ok) return
  busy.value = true
  try {
    const res = await bridge().app.ApplyBackup(pickedPath.value, overwrite.value)
    toast('info', `已恢复 ${res.written} 个文件（跳过 ${res.skipped} 个）· 请重启应用`)
    emit('close')
  } catch (e) {
    toast('error', errText(e))
  } finally {
    busy.value = false
  }
}

function fmtBytes(n: number): string {
  if (n <= 0) return '—'
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`
  return `${Math.max(1, Math.round(n / 1024))} KB`
}

function fmtTime(ms: number): string {
  if (!ms) return '未知时间'
  return new Date(ms).toLocaleString()
}
</script>

<template>
  <BaseModal open title="备份与恢复" @close="emit('close')">
    <div class="space-y-4 text-xs">
      <!-- 导出 -->
      <section class="rounded-lg border border-[var(--c-border)] p-3">
        <h3 class="text-sm font-medium text-[var(--c-text)]">导出备份</h3>
        <p class="mt-1 text-[var(--c-text-dim)]">
          把会话、记忆、渠道、语气、扩展与设置打成一个 zip。换机/重装前先导一份。
        </p>
        <button class="btn-primary mt-2 px-3 py-1.5" :disabled="busy" @click="doExport">
          {{ busy ? '处理中…' : '选择位置并导出' }}
        </button>
      </section>

      <!-- 导入 -->
      <section class="rounded-lg border border-[var(--c-border)] p-3">
        <h3 class="text-sm font-medium text-[var(--c-text)]">恢复备份</h3>
        <p class="mt-1 text-[var(--c-text-dim)]">
          选一个 .zip 备份。先给你看摘要，确认后才写入；写入前自动给当前数据留一份 .bak。
        </p>
        <button class="chip mt-2 px-3 py-1.5" :disabled="busy" @click="doPick">
          选择备份文件…
        </button>

        <div v-if="preview" class="mt-2 rounded-[var(--r-input)] bg-[var(--c-surface-soft)] p-2">
          <template v-if="preview.valid">
            <p class="text-[var(--c-text)]">
              {{ preview.fileCount }} 个文件 · {{ preview.sessionCount }} 场会话 ·
              {{ fmtBytes(preview.totalBytes) }}
            </p>
            <p class="mt-0.5 text-[var(--c-text-faint)]">
              来自 {{ preview.version || '未知版本' }} · {{ fmtTime(preview.exportedAt) }}
            </p>
            <label class="mt-2 flex items-center gap-1.5 text-[var(--c-text-dim)]">
              <input v-model="overwrite" type="checkbox" />
              覆盖同名数据（不勾 = 只补缺失的文件）
            </label>
            <button class="btn-primary mt-2 px-3 py-1.5" :disabled="busy" @click="doImport">
              恢复…
            </button>
          </template>
          <template v-else>
            <p class="text-[var(--c-err-text)]">这个文件不能作为备份使用：{{ preview.reason }}</p>
          </template>
        </div>
      </section>

      <p class="text-[11px] text-[var(--c-text-faint)]">
        备份里含渠道密钥（明文，与本机存储形态一致）——请像对待账本一样保管导出的 zip。
      </p>
    </div>
  </BaseModal>
</template>
