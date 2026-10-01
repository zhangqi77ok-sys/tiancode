<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useChatStore } from '../stores/chat'
import { useEscClose } from '../composables/useEsc'
import { errText } from '../composables/errText'
import { useToast } from '../composables/useToast'
import { bridge } from '../wails'
import BaseModal from './BaseModal.vue'

// 工作区设置（第 8 批）：只放两项**可选**命令——「在这一行打开」与「检查命令」。
// 都留空 = 不启用：空检查命令任何时候都不跑（绝不猜 go test），
// 空 openAtLine 与系统默认程序打开逐字一致。
const emit = defineEmits<{ (e: 'close'): void }>()
const store = useChatStore()
const { push: toast } = useToast()
const openAtLine = ref('')
const checkCommand = ref('')
const error = ref('')
const saving = ref(false)
const loaded = ref(false)

useEscClose(ref(true), () => emit('close'))

onMounted(async () => {
  try {
    const s = await bridge().app.WorkspaceSettings(store.sessionId)
    openAtLine.value = s?.openAtLine ?? ''
    checkCommand.value = s?.checkCommand ?? ''
  } catch (e) {
    error.value = errText(e)
  } finally {
    loaded.value = true
  }
})

async function save() {
  if (saving.value) return
  saving.value = true
  error.value = ''
  try {
    await bridge().app.SaveWorkspaceSettings(store.sessionId, {
      openAtLine: openAtLine.value.trim(),
      checkCommand: checkCommand.value.trim(),
    })
    toast('info', '已保存到本工作区')
    emit('close')
  } catch (e) {
    error.value = errText(e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <BaseModal open title="工作区设置" panel-class="w-full max-w-[560px]" @close="emit('close')">
    <div class="space-y-4 p-5">
      <p class="text-xs text-[var(--c-text-dim)]">
        只作用于当前对话所属的工作区（按绝对路径记在 workspace-settings.json）。两项都留空 = 不启用。
      </p>
      <p v-if="error" class="text-xs text-[var(--c-err-text)]">{{ error }}</p>

      <label class="block space-y-1.5">
        <span class="text-xs font-medium">在这一行打开</span>
        <input
          v-model="openAtLine"
          class="field-input font-mono text-xs"
          placeholder="code -g {path}:{line}"
          :disabled="!loaded"
        />
        <span class="block text-[11px] text-[var(--c-text-faint)]">
          按空格拆成命令与参数，{path} / {line} 会被替换；不经 shell，也不替你加引号。
          留空则用系统默认程序打开（只打开文件，不定位到行）。
        </span>
      </label>

      <label class="block space-y-1.5">
        <span class="text-xs font-medium">检查命令</span>
        <input
          v-model="checkCommand"
          class="field-input font-mono text-xs"
          placeholder="go test ./..."
          :disabled="!loaded"
        />
        <span class="block text-[11px] text-[var(--c-text-faint)]">
          留空则任何时候都不跑。非空时在回合结束后（以及「应用到文件」写入后）各跑一次；
          输出里的 path:line 在界面上可点。
        </span>
      </label>

      <div class="flex justify-end gap-2">
        <button class="chip" @click="emit('close')">取消</button>
        <button class="btn-primary px-4 py-2 text-sm" :disabled="saving || !loaded" @click="save">
          {{ saving ? '保存中…' : '保存' }}
        </button>
      </div>
    </div>
  </BaseModal>
</template>
