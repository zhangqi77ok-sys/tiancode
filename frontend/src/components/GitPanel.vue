<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useChatStore } from '../stores/chat'
import { useDialogs } from '../composables/useDialogs'
import { useToast } from '../composables/useToast'
import { errText } from '../composables/errText'
import { bridge, type GitStatusEntryDTO } from '../wails'
import AppIcon from './AppIcon.vue'

// 右栏「Git」tab（0.0.24）：变更文件清单 + 单文件 diff + 提交流。
// 后端只读 status/diff，写路径复用既有「生成提交说明 → 确认 → git add -A + commit」
// 链路（模型生成、人工确认，git push/reset/clean 仍无实现路径）。

const store = useChatStore()
const dialogs = useDialogs()
const { push: toast } = useToast()

const entries = ref<GitStatusEntryDTO[]>([])
const loading = ref(false)
const error = ref('')
const selected = ref('')
const diff = ref('')
const diffLoading = ref(false)
let diffSeq = 0

const untracked = ref<GitStatusEntryDTO[]>([])
const tracked = ref<GitStatusEntryDTO[]>([])

function splitEntries() {
  untracked.value = entries.value.filter((e) => e.untracked)
  tracked.value = entries.value.filter((e) => !e.untracked)
}

async function load() {
  if (!store.sessionId) {
    entries.value = []
    error.value = ''
    return
  }
  loading.value = true
  error.value = ''
  try {
    entries.value = (await bridge().app.GitStatusFiles(store.sessionId)) ?? []
    splitEntries()
  } catch (e) {
    error.value = errText(e)
    entries.value = []
  } finally {
    loading.value = false
  }
}

async function openFile(e: GitStatusEntryDTO) {
  if (e.untracked) {
    // 未跟踪文件没有 HEAD diff：走文件详情面板看全文（既有只读浏览）
    store.openFileDetail(e.path)
    return
  }
  selected.value = e.path
  diffLoading.value = true
  const seq = ++diffSeq
  try {
    const out = (await bridge().app.GitFileDiff(store.sessionId, e.path)) ?? ''
    if (seq !== diffSeq) return
    diff.value = out || '（无未暂存改动——可能已暂存）'
  } catch (err) {
    if (seq !== diffSeq) return
    diff.value = ''
    toast('error', errText(err))
  } finally {
    if (seq === diffSeq) diffLoading.value = false
  }
}

async function commitFlow() {
  if (!store.sessionId) return
  let message = ''
  try {
    message = await store.suggestCommitMessage()
  } catch (e) {
    toast('error', errText(e))
    return
  }
  const next = await dialogs.prompt({
    title: '提交变更',
    message: '提交说明（已按本轮 diff 生成，可修改）：',
    value: message || '',
    maxlength: 200,
  })
  if (next === null || !next.trim()) return
  try {
    const out = await store.gitStageAndCommit(next.trim())
    toast('info', out ? `已提交：${out}` : '已提交')
    await load()
  } catch (e) {
    toast('error', errText(e))
  }
}

function statusBadge(e: GitStatusEntryDTO): string {
  if (e.untracked) return '??'
  return (e.x + e.y).trim() || 'M'
}

onMounted(load)

// 数据跟手（0.0.29，用户实机反馈"右栏里面的内容没有真实效果"）：此前只在 onMounted
// 读一次，面板开着之后发生的改动（模型写文件、切换会话换工作区）永远看不到——
// 数据是真的但停在打开那一瞬间，用户会当成"假数据"。与「目录」面板同款纪律
// （watch treeRev / sessionId），这里不引入轮询：写文件必有 treeRev 信号。
watch(
  () => `${store.sessionId}:${store.treeRev}`,
  () => void load(),
)
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col" aria-label="Git">
    <div class="flex items-center gap-2 border-b border-[var(--c-border)] px-3 py-2 text-xs">
      <AppIcon name="refresh" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
      <span class="text-[var(--c-text-dim)]">工作区变更</span>
      <span v-if="entries.length" class="tabular-nums text-[10px] text-[var(--c-text-faint)]">{{ entries.length }}</span>
      <button class="ml-auto shrink-0 text-[var(--c-text-faint)] hover:text-[var(--c-primary)]" aria-label="刷新" @click="load">
        <AppIcon name="refresh" :size="12" />
      </button>
    </div>

    <p v-if="error" class="px-3 py-3 text-xs text-[var(--c-err-text)]">{{ error }}</p>
    <p v-else-if="loading" class="px-3 py-3 text-xs text-[var(--c-text-faint)]">载入中…</p>
    <p v-else-if="!entries.length" class="px-3 py-6 text-xs text-[var(--c-text-faint)]">
      工作区干净，没有未提交的变更。
    </p>

    <div v-else class="min-h-0 flex-1 overflow-y-auto px-2 py-2 text-xs">
      <div v-if="untracked.length" class="mb-2">
        <p class="px-1 pb-1 text-[10px] text-[var(--c-text-faint)]">未跟踪（点击看全文）</p>
        <button
          v-for="e in untracked"
          :key="e.path"
          class="block w-full truncate rounded-lg px-2 py-1 text-left text-[var(--c-text-dim)] transition-colors hover:bg-[var(--c-surface-soft)] hover:text-[var(--c-primary)]"
          :title="e.path"
          @click="openFile(e)"
        >
          <span class="mr-1.5 text-[10px] text-[var(--c-text-faint)]">??</span>{{ e.path }}
        </button>
      </div>
      <div v-if="tracked.length">
        <p class="px-1 pb-1 text-[10px] text-[var(--c-text-faint)]">已修改（点击看 diff）</p>
        <button
          v-for="e in tracked"
          :key="e.path"
          class="block w-full truncate rounded-lg px-2 py-1 text-left transition-colors"
          :class="selected === e.path ? 'bg-[var(--c-primary-soft)] text-[var(--c-primary)]' : 'text-[var(--c-text-dim)] hover:bg-[var(--c-surface-soft)] hover:text-[var(--c-primary)]'"
          :title="e.path"
          @click="openFile(e)"
        >
          <span class="mr-1.5 text-[10px] text-[var(--c-text-faint)]">{{ statusBadge(e) }}</span>{{ e.path }}
        </button>
      </div>
    </div>

    <!-- diff 预览（点已修改文件出现） -->
    <div v-if="diff" class="max-h-[45%] shrink-0 overflow-y-auto border-t border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2">
      <p class="mb-1 truncate text-[10px] text-[var(--c-text-faint)]" :title="selected">{{ selected }}</p>
      <pre class="whitespace-pre-wrap break-all font-mono text-[10px] leading-4 text-[var(--c-text-dim)]">{{ diff }}</pre>
    </div>

    <div class="flex shrink-0 items-center gap-2 border-t border-[var(--c-border)] px-3 py-2">
      <button
        class="btn-primary flex-1 justify-center py-1.5 text-xs"
        :disabled="!entries.length || store.running"
        title="按当前工作区 diff 生成提交说明，确认后执行 git add -A + commit"
        @click="commitFlow"
      >
        <AppIcon name="pencil" :size="12" /> 生成提交说明并提交
      </button>
    </div>
  </section>
</template>
