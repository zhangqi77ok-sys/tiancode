<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useChatStore, type ChatMsg } from '../stores/chat'
import { useToast } from '../composables/useToast'
import { registerEsc } from '../composables/useEsc'
import { diffLineClass, diffStat } from '../composables/diffView'
import { bridge } from '../wails'
import AppIcon from './AppIcon.vue'

// 文件详情面板（第 3 批）：右侧专区看一个文件的全部改动——逐次 diff + 累计统计 +
// 恢复写入前 / 资源管理器。打开来源：本轮变更的文件行、工具卡文件名。
// Esc 关闭（经 Esc 消费栈，绝不顺手中断生成——see composables/useEsc.ts）。

const store = useChatStore()
const { push: toast } = useToast()

const path = computed(() => store.fileDetailPath)

// 该文件在本会话的全部 write/edit 卡（按发生顺序，统计预计算避免模板重复遍历）
interface DetailCard {
  m: ChatMsg
  add: number
  del: number
}
const cards = computed<DetailCard[]>(() =>
  store.messages
    .filter((m) => m.role === 'tool' && (m.op === 'write' || m.op === 'edit') && (m.title || '').trim() === path.value)
    .map((m) => ({ m, ...diffStat(m.diff) })),
)

// 路径两段式（与列表/工具卡同规则）：目录暗、末段亮
const parts = computed(() => {
  const p = path.value
  const i = p.lastIndexOf('/')
  return i > 0 ? { dir: p.slice(0, i + 1), base: p.slice(i + 1) } : { dir: '', base: p }
})

const total = computed(() =>
  cards.value.reduce(
    (acc, c) => {
      acc.add += c.add
      acc.del += c.del
      return acc
    },
    { add: 0, del: 0 },
  ),
)

// Esc：面板挂载 = 打开（v-if 控制），mounted 注册即"打开时注册"
let offEsc: (() => void) | null = null
onMounted(() => {
  offEsc = registerEsc(() => store.closeFileDetail())
})
onBeforeUnmount(() => offEsc?.())

// 只读正文（第 8 批）：路径按**这场对话**的工作区解析（后端 ResolveSessionPath）。
// 面板只读不写：没有编辑、没有保存。超限只给前半，照实写明被截断。
const body = ref('')
const bodyNote = ref('')
const bodyLoading = ref(false)
async function loadBody() {
  bodyLoading.value = true
  body.value = ''
  bodyNote.value = ''
  try {
    const res = await bridge().app.ReadSessionFile(store.sessionId, path.value)
    body.value = res?.content ?? ''
    if (res?.truncated) {
      const mb = Math.round((res.limit / (1024 * 1024)) * 10) / 10
      bodyNote.value = `正文超过单次读取上限（${mb} MB），只显示前 ${mb} MB`
    }
  } catch (e) {
    bodyNote.value = String(e instanceof Error ? e.message : e)
  } finally {
    bodyLoading.value = false
  }
}
// 换文件 / 换会话都要重取（会话不同 → 工作区不同 → 同一个相对路径可能是别的文件）
watch([path, () => store.sessionId], () => void loadBody(), { immediate: true })

const restoring = ref<string | null>(null)
async function restore(m: ChatMsg) {
  if (!m.callId || restoring.value) return
  restoring.value = m.callId
  await store.restoreWrite(m.callId, m.undoPath)
  restoring.value = null
}

// 路径按**这场对话**的工作区解析（0.0.11）：传当前会话 ID，不用顶栏"下一场"的根
async function reveal() {
  try {
    await bridge().app.RevealInExplorer(store.sessionId, path.value)
  } catch (e) {
    toast('error', String(e instanceof Error ? e.message : e))
  }
}

// 用系统默认程序打开（0.0.11）：失败同样走 toast，绝不静默
async function openDefault() {
  try {
    await bridge().app.OpenInDefaultApp(store.sessionId, path.value)
  } catch (e) {
    toast('error', String(e instanceof Error ? e.message : e))
  }
}
</script>

<template>
  <aside
    class="flex w-[420px] shrink-0 flex-col border-l border-[var(--c-border)] xl:w-[480px]"
    aria-label="文件详情"
  >
    <!-- 头部：路径两段式 + 资源管理器 + 关闭（Esc） -->
    <div class="flex items-center gap-2 border-b border-[var(--c-border)] px-3 py-2.5">
      <AppIcon name="file" :size="14" class="shrink-0 text-[var(--c-text-faint)]" />
      <div class="flex min-w-0 flex-1 items-center text-sm font-medium" :title="path">
        <span v-if="parts.dir" class="min-w-0 truncate text-[var(--c-text-faint)]">{{ parts.dir }}</span>
        <span class="shrink-0 text-[var(--c-text)]">{{ parts.base }}</span>
      </div>
      <button class="btn-icon" title="在资源管理器中显示" @click="reveal">
        <AppIcon name="folder" :size="14" />
      </button>
      <button class="btn-icon" title="用系统默认程序打开" @click="openDefault">
        <AppIcon name="external" :size="14" />
      </button>
      <button class="btn-icon" title="关闭（Esc）" @click="store.closeFileDetail()">
        <AppIcon name="x" :size="14" />
      </button>
    </div>

    <!-- 汇总：改动次数 + 累计增删 -->
    <div class="flex items-center gap-3 px-3 py-2 text-xs text-[var(--c-text-dim)]">
      <span>{{ cards.length }} 处改动</span>
      <span class="font-mono">
        <span class="text-[var(--c-ok-text)]">+{{ total.add }}</span>
        <span class="ml-1 text-[var(--c-err-text)]">-{{ total.del }}</span>
      </span>
    </div>

    <div class="min-h-0 flex-1 space-y-3 overflow-y-auto px-3 pb-3">
      <!-- 只读正文（第 8 批）：本会话是否改过都显示——先看文件现在长什么样，
           再看本轮改了什么。只读，不提供编辑/保存 -->
      <div class="flex flex-col gap-1 pt-1">
        <div class="flex items-center gap-2 text-[11px] text-[var(--c-text-faint)]">
          <span class="stat px-1.5 py-0.5">正文（只读）</span>
          <span v-if="bodyNote" class="min-w-0 truncate" :title="bodyNote">{{ bodyNote }}</span>
          <span v-else-if="bodyLoading" class="text-[var(--c-text-faint)]">读取中…</span>
          <span v-else class="ml-auto tabular-nums">{{ body ? `${body.split('\n').length} 行` : '' }}</span>
        </div>
        <!-- 有正文才渲染内容块：读不到时上面那行已写明原因，绝不用空白冒充已读 -->
        <div
          v-if="body"
          class="max-h-[42vh] overflow-auto whitespace-pre-wrap rounded-lg border border-[var(--c-border)] bg-[var(--c-surface)] px-2.5 py-1.5 font-mono text-xs leading-5"
        >
          {{ body }}
        </div>
      </div>

      <div v-if="!cards.length" class="py-6 text-center text-xs text-[var(--c-text-faint)]">
        该文件在本会话没有变更记录
      </div>
      <div v-for="(c, i) in cards" :key="c.m.callId || c.m.id || i" class="flex flex-col gap-1">
        <div class="flex items-center gap-2 text-[11px] text-[var(--c-text-faint)]">
          <span class="stat px-1.5 py-0.5">{{ c.m.op === 'write' ? '写入' : '修改' }}</span>
          <span>第 {{ i + 1 }} / {{ cards.length }} 处</span>
          <span class="ml-auto font-mono">
            <span class="text-[var(--c-ok-text)]">+{{ c.add }}</span>
            <span class="ml-1 text-[var(--c-err-text)]">-{{ c.del }}</span>
          </span>
        </div>
        <div
          class="max-h-[42vh] overflow-auto whitespace-pre rounded-lg border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-2.5 py-1.5 font-mono text-xs leading-5"
        >
          <div v-for="(l, li) in (c.m.diff || '').split('\n')" :key="li" :class="diffLineClass(l)">{{ l }}</div>
        </div>
        <div v-if="c.m.hasUndo" class="flex gap-1.5">
          <button
            class="chip text-[11px]"
            :disabled="restoring === c.m.callId"
            title="把文件写回这次修改之前的内容（文件后来被改过时会被拒绝）"
            @click="restore(c.m)"
          >
            <AppIcon name="refresh" :size="11" />
            {{ restoring === c.m.callId ? '正在恢复…' : '恢复写入前' }}
          </button>
        </div>
      </div>
    </div>
  </aside>
</template>
