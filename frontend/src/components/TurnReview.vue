<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue'
import { useChatStore, type ChatMsg } from '../stores/chat'
import { useToast } from '../composables/useToast'
import { bridge } from '../wails'
import AppIcon from './AppIcon.vue'

// 本轮变更审查带（0.0.09 驾驶舱）：编码时人要核对的是"这一轮改了哪些文件"——
// 那些 write/edit 卡原本散落在消息流中间要往上翻。现在一条带子收拢在输入框
// 上方：每个改动一行，点开就是那份 diff，旁边是已有的"恢复写入前"。
// 不做完整编辑器/文件树/Monaco——先做这条审查带，比做文件树值。

const store = useChatStore()
const { push: toast } = useToast()

// 本轮 = 最后一条 user 消息之后的成功 write/edit 卡（进行中实时出现，不必等终态）
const changes = computed<ChatMsg[]>(() => {
  const msgs = store.messages
  let lastUser = -1
  for (let i = msgs.length - 1; i >= 0; i--) {
    if (msgs[i].role === 'user') {
      lastUser = i
      break
    }
  }
  return msgs.filter(
    (m, i) =>
      i > lastUser && m.role === 'tool' && m.status === 'success' && (m.op === 'write' || m.op === 'edit') && !!m.callId,
  )
})

// 展开：点击行开合；外部聚焦（工具卡点文件名）强制展开并滚动到位
const expanded = ref<Set<string>>(new Set())
function toggle(id: string) {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}

const rowsEl = ref<HTMLElement | null>(null)
watch(
  () => store.reviewFocus,
  async (id) => {
    if (!id) return
    const next = new Set(expanded.value)
    next.add(id)
    expanded.value = next
    await nextTick()
    document.getElementById(`review-row-${id}`)?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
    store.reviewFocus = null
  },
)

// diff 行着色（与 ToolCard 同规则）
function diffLineClass(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'text-[var(--c-text-dim)]'
  if (line.startsWith('@@')) return 'text-[var(--c-primary)]'
  if (line.startsWith('+')) return 'text-[var(--c-ok-text)]'
  if (line.startsWith('-')) return 'text-[var(--c-err-text)]'
  return 'text-[var(--c-text-dim)]'
}

// stat 与次要动作（与 ToolCard 一致）
function statOf(m: ChatMsg) {
  let add = 0
  let del = 0
  for (const line of (m.diff || '').split('\n')) {
    if (line.startsWith('+++') || line.startsWith('---')) continue
    if (line.startsWith('+')) add++
    else if (line.startsWith('-')) del++
  }
  return { add, del }
}

const restoring = ref<string | null>(null)
async function restore(m: ChatMsg) {
  if (!m.callId || restoring.value) return
  restoring.value = m.callId
  await store.restoreWrite(m.callId, m.undoPath)
  restoring.value = null
}

async function reveal(m: ChatMsg) {
  try {
    await bridge().app.RevealInExplorer(m.title?.trim() || '')
  } catch (e) {
    toast('error', String(e instanceof Error ? e.message : e))
  }
}

// 标题：纯文件名（title 可能是带斜杠的相对路径——取末段，完整路径在 hover）
function fileName(m: ChatMsg): string {
  const t = m.title || ''
  const segs = t.split(/[\\/]/)
  return segs[segs.length - 1] || t
}
</script>

<template>
  <div
    v-if="changes.length"
    ref="rowsEl"
    class="mx-1 mb-1.5 rounded-xl border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-2 py-1.5"
    aria-label="本轮变更"
  >
    <div class="mb-1 flex items-center gap-1.5 px-1 text-[11px] text-[var(--c-text-faint)]">
      <AppIcon name="pencil" :size="11" /> 本轮变更（{{ changes.length }}）
    </div>
    <div class="max-h-52 space-y-0.5 overflow-y-auto">
      <div v-for="m in changes" :id="`review-row-${m.callId}`" :key="m.callId">
        <button
          class="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-xs transition-colors hover:bg-[var(--c-surface)]"
          :aria-expanded="expanded.has(m.callId!)"
          @click="toggle(m.callId!)"
        >
          <span class="h-1.5 w-1.5 shrink-0 rounded-full bg-[var(--c-ok)]"></span>
          <span class="min-w-0 truncate font-medium text-[var(--c-text)]" :title="m.title">{{ fileName(m) }}</span>
          <span class="shrink-0 rounded bg-[var(--c-primary-soft)] px-1.5 py-0.5 text-[10px] text-[var(--c-primary)]">
            {{ m.op === 'write' ? '新建' : '修改' }}
          </span>
          <span v-if="m.diff" class="shrink-0 font-mono text-[11px]">
            <span class="text-[var(--c-ok-text)]">+{{ statOf(m).add }}</span>
            <span class="ml-1 text-[var(--c-err-text)]">-{{ statOf(m).del }}</span>
          </span>
          <AppIcon
            name="chevron-down"
            :size="12"
            class="ml-auto shrink-0 text-[var(--c-text-faint)] transition-transform"
            :class="expanded.has(m.callId!) ? '' : '-rotate-90'"
          />
        </button>
        <template v-if="expanded.has(m.callId!)">
          <div
            class="mx-2 mb-1 max-h-60 overflow-auto whitespace-pre rounded-lg border border-[var(--c-border)] bg-[var(--c-surface)] px-2.5 py-1.5 font-mono text-xs leading-5"
          >
            <div v-for="(l, li) in (m.diff || '').split('\n')" :key="li" :class="diffLineClass(l)">{{ l }}</div>
          </div>
          <div class="mb-1 flex gap-1.5 px-2">
            <button
              v-if="m.hasUndo"
              class="chip text-[11px]"
              :disabled="restoring === m.callId"
              title="把文件写回这次修改之前的内容"
              @click="restore(m)"
            >
              <AppIcon name="refresh" :size="11" />
              {{ restoring === m.callId ? '正在恢复…' : '恢复写入前' }}
            </button>
            <button class="chip text-[11px]" title="打开文件所在目录并选中（次要动作）" @click="reveal(m)">
              <AppIcon name="file" :size="11" /> 资源管理器
            </button>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>
