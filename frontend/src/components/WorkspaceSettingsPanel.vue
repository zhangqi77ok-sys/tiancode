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
// shell 审批白名单（0.0.24）：一行一条前缀；shell 命中以这些前缀开头时免审批
const shellAllowText = ref('')
// shell 默认超时（0.0.25）：这个工作区里不指定时的默认；0 = 内置 120s
const shellTimeout = ref(0)
// 工作区快捷命令（0.0.26）：三槽（build/test/run）——只存命令，执行走既有命令行链
const quick = ref({ build: '', test: '', run: '' })
const error = ref('')
const saving = ref(false)
const loaded = ref(false)

useEscClose(ref(true), () => emit('close'))

onMounted(async () => {
  try {
    const s = await bridge().app.WorkspaceSettings(store.sessionId)
    openAtLine.value = s?.openAtLine ?? ''
    checkCommand.value = s?.checkCommand ?? ''
    shellAllowText.value = (s?.shellAllow ?? []).join('\n')
    shellTimeout.value = s?.shellTimeoutSeconds ?? 0
    quick.value = {
      build: s?.quickCommands?.build ?? '',
      test: s?.quickCommands?.test ?? '',
      run: s?.quickCommands?.run ?? '',
    }
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
      shellAllow: shellAllowText.value
        .split('\n')
        .map((l) => l.trim())
        .filter(Boolean),
      shellTimeoutSeconds: Math.max(0, Math.min(600, shellTimeout.value || 0)),
      quickCommands: {
        build: quick.value.build.trim(),
        test: quick.value.test.trim(),
        run: quick.value.run.trim(),
      },
    })
    toast('info', '已保存到本工作区')
    store.bumpWsSettings() // 快捷命令等派生视图立即重拉（否则要切会话才现形）
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

      <label class="block">
        <span class="mb-1 block text-xs text-[var(--c-text-dim)]">Shell 审批白名单（每行一条命令前缀）</span>
        <textarea
          v-model="shellAllowText"
          rows="3"
          class="field-input font-mono text-xs"
          placeholder="git status&#10;go test ./..."
          :disabled="!loaded"
        ></textarea>
        <span class="block text-[11px] text-[var(--c-text-faint)]">
          命令确认写着时，以这些前缀开头的 shell 命令自动放行不再弹卡（如 git status、go build）；
          未命中的照常确认。只在本工作区生效，只能放行、不能反向加严。
        </span>
      </label>

      <label class="block space-y-1.5">
        <span class="text-xs font-medium">Shell 默认超时（秒）</span>
        <input
          v-model.number="shellTimeout"
          type="number"
          min="0"
          max="600"
          step="30"
          class="field-input text-xs"
          placeholder="120（留空或 0 = 内置默认）"
          :disabled="!loaded"
        />
        <span class="block text-[11px] text-[var(--c-text-faint)]">
          这个工作区里跑 shell 命令的默认时限（0 = 内置 120 秒，上限 600 秒）。大仓库的
          go build / npm install 120 秒不够用；模型仍可在单条命令上指定更短的超时。
        </span>
      </label>

      <!-- 快捷命令（0.0.26）：编译主链路三步一键跑；执行走既有命令行链（含审批） -->
      <div class="space-y-1.5">
        <span class="text-xs font-medium">快捷命令（一键跑）</span>
        <div v-for="slot in (['build', 'test', 'run'] as const)" :key="slot" class="flex items-center gap-2">
          <span class="w-10 shrink-0 text-[11px] text-[var(--c-text-dim)]">{{ slot }}</span>
          <input
            v-model="quick[slot]"
            class="field-input flex-1 font-mono text-xs"
            :placeholder="slot === 'build' ? 'go build ./...' : slot === 'test' ? 'go test ./...' : 'go run .'"
            :disabled="!loaded"
          />
        </div>
        <span class="block text-[11px] text-[var(--c-text-faint)]">
          填好后输入框下方一键跑（编译/测试/运行）。执行走的是与模型同一条命令行与审批闸门——
          命令确认开着时同样会先问你。留空则不显示该按钮。
        </span>
      </div>

      <div class="flex justify-end gap-2">
        <button class="chip" @click="emit('close')">取消</button>
        <button class="btn-primary px-4 py-2 text-sm" :disabled="saving || !loaded" @click="save">
          {{ saving ? '保存中…' : '保存' }}
        </button>
      </div>
    </div>
  </BaseModal>
</template>
