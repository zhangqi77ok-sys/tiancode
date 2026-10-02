<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useChatStore } from '../stores/chat'
import { useDialogs } from '../composables/useDialogs'
import { useToast } from '../composables/useToast'
import { bridge, type RoundInfoDTO } from '../wails'
import { errText } from '../composables/errText'
import AppIcon from './AppIcon.vue'

// 右栏「时间线」tab（0.0.20）：历轮一览 + 「回滚到此轮之前」。
// 数据源 = 后端账本扫描（RoundTimeline），不依赖当前消息缓冲——长会话也能看全史。
// 回滚语义与「重跑」的文件恢复完全一致（该轮及其后改过的文件回到锚点时状态，
// 同文件取最早检查点），唯一区别是**保留对话历史**（不 fork、不重发）。
const store = useChatStore()
const dialogs = useDialogs()
const { push: toast } = useToast()

const rounds = ref<RoundInfoDTO[]>([])
const loading = ref(false)
const error = ref('')
const busySeq = ref<number | null>(null) // 正在回滚的锚点（防双击）

// 点击轮次正文 → 跳到消息流对应位置（0.0.21）：回滚前先看清那轮干了什么。
// 定位（含长会话补页）由 store.jumpToSeq 负责，面板只发信号。
const emit = defineEmits<{ (e: 'jump', userSeq: number): void }>()

async function load() {
  if (!store.sessionId) {
    rounds.value = []
    return
  }
  loading.value = true
  error.value = ''
  try {
    rounds.value = (await bridge().app.RoundTimeline(store.sessionId)) ?? []
  } catch (e) {
    error.value = errText(e)
    rounds.value = []
  } finally {
    loading.value = false
  }
}

async function revertTo(r: RoundInfoDTO) {
  const ok = await dialogs.confirm({
    title: '回滚到此轮之前',
    message: `把工作区文件恢复到第 ${r.round} 轮开始之前的状态？\n该轮及其后改过的文件都会被恢复（对话历史保留）。`,
    confirmText: '回滚',
    danger: true,
  })
  if (!ok) return
  busySeq.value = r.userSeq
  try {
    const res = await bridge().app.RevertToRound(store.sessionId, r.userSeq)
    const restored = res?.restored ?? []
    const skipped = res?.skipped ?? []
    if (skipped.length) {
      await dialogs.confirm({ title: '部分文件撤不回', message: skipped.join('\n'), confirmText: '知道了' })
    } else if (restored.length) {
      toast('info', `已回滚 ${restored.length} 个文件（对话历史保留）`)
    }
    await load() // 可撤标记随之刷新
  } catch (e) {
    toast('error', errText(e))
  } finally {
    busySeq.value = null
  }
}

defineExpose({ load })
onMounted(load)
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col" aria-label="时间线">
    <div class="flex items-center gap-2 border-b border-[var(--c-border)] px-3 py-2 text-xs">
      <AppIcon name="refresh" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
      <span class="text-[var(--c-text-dim)]">历轮改动（按用户消息分轮）</span>
      <button class="ml-auto shrink-0 text-[var(--c-text-faint)] hover:text-[var(--c-primary)]" aria-label="刷新" @click="load">
        <AppIcon name="refresh" :size="12" />
      </button>
    </div>

    <p v-if="error" class="px-3 py-3 text-xs text-[var(--c-err-text)]">{{ error }}</p>
    <p v-else-if="loading" class="px-3 py-3 text-xs text-[var(--c-text-faint)]">载入中…</p>
    <p v-else-if="!rounds.length" class="px-3 py-6 text-xs text-[var(--c-text-faint)]">
      还没有轮次——发第一条消息后，这里按轮列出每轮改了哪些文件。
    </p>

    <div v-else class="min-h-0 flex-1 overflow-y-auto px-2 py-2">
      <!-- 新轮在上（最近的操作离手最近） -->
      <div v-for="r in [...rounds].reverse()" :key="r.userSeq" class="mb-2 rounded-lg border border-[var(--c-border)] p-2">
        <div class="flex items-baseline gap-2">
          <span class="shrink-0 text-[10px] font-medium text-[var(--c-primary)]">第 {{ r.round }} 轮</span>
          <button
            class="min-w-0 flex-1 truncate text-left text-xs text-[var(--c-text-dim)] transition-colors hover:text-[var(--c-primary)]"
            :title="`${r.text || '（无正文）'}（点击跳到消息流）`"
            @click="emit('jump', r.userSeq)"
          >
            {{ r.text || '（无正文）' }}
          </button>
        </div>
        <div v-if="r.files.length" class="mt-1 space-y-0.5">
          <p
            v-for="f in r.files"
            :key="f.path"
            class="truncate text-[11px]"
            :class="f.revertable ? 'text-[var(--c-text-dim)]' : 'text-[var(--c-text-faint)] line-through'"
            :title="f.revertable ? f.path : `${f.path}（已回滚过）`"
          >
            {{ f.path }}
          </p>
        </div>
        <p v-else class="mt-1 text-[11px] text-[var(--c-text-faint)]">本轮没有文件改动</p>
        <button
          class="mt-1.5 rounded-lg border border-[var(--c-border)] px-2 py-1 text-[11px] text-[var(--c-text-dim)] transition-colors hover:border-[var(--c-warn)] hover:text-[var(--c-warn-text)] disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="busySeq !== null || store.running"
          :title="store.running ? '会话运行中不能回滚' : '把工作区文件恢复到这一轮开始之前（对话历史保留）'"
          @click="revertTo(r)"
        >
          {{ busySeq === r.userSeq ? '回滚中…' : '回滚到此轮之前' }}
        </button>
      </div>
    </div>
  </section>
</template>
