<script setup lang="ts">
import { computed, ref } from 'vue'
import { useChatStore, type ChatMsg } from '../stores/chat'
import { diffStat } from '../composables/diffView'
import { useDialogs } from '../composables/useDialogs'
import { errText } from '../composables/errText'
import { useToast } from '../composables/useToast'
import AppIcon from './AppIcon.vue'

// 本轮变更审查带（0.0.09 驾驶舱；第 2/3 批改造）：write/edit 收拢在输入框上方，
// 默认折叠为一行汇总；展开后按文件聚合，点文件行 → 右侧文件详情面板看 diff
//（diff 是主视图，不再挤在消息流与审查带里两处滚动）。

const store = useChatStore()

// 本轮 = 最后一条 user 消息之后的成功 write/edit 卡（进行中实时出现，不必等终态）。
// 不要求 callId：代码块「应用到文件」的本地写入卡（无 callId）同样属于本轮变更。
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
    (m, i) => i > lastUser && m.role === 'tool' && m.status === 'success' && (m.op === 'write' || m.op === 'edit'),
  )
})

// 按文件聚合：同一路径的多次改动合并为一行（改 N 次 + 累计 ±）——同文件被 replace
// 多次时不再出现一排同名条目；路径拆两段（目录暗、末段亮）保证同名文件可区分。
interface FileGroup {
  path: string
  dir: string
  base: string
  count: number
  add: number
  del: number
}
const groups = computed<FileGroup[]>(() => {
  const out: FileGroup[] = []
  const byPath = new Map<string, FileGroup>()
  for (const m of changes.value) {
    const path = (m.title || '').trim() || '(未知文件)'
    let g = byPath.get(path)
    if (!g) {
      const i = path.lastIndexOf('/')
      g = {
        path,
        dir: i > 0 ? path.slice(0, i + 1) : '',
        base: i > 0 ? path.slice(i + 1) : path,
        count: 0,
        add: 0,
        del: 0,
      }
      byPath.set(path, g)
      out.push(g)
    }
    const s = diffStat(m.diff)
    g.count++
    g.add += s.add
    g.del += s.del
  }
  return out
})

const totalAdd = computed(() => groups.value.reduce((n, g) => n + g.add, 0))
const totalDel = computed(() => groups.value.reduce((n, g) => n + g.del, 0))

// 面板默认折叠（用户要求）：只显示「本轮变更 · N 个文件 +A -D」标题行；折叠态记忆到
// localStorage（跨重启保留用户偏好，与 FloatingTodo 同纪律）。
const PANEL_KEY = 'tiancode.turnReview.panelOpen'
const panelOpen = ref(localStorage.getItem(PANEL_KEY) === '1')
function togglePanel() {
  panelOpen.value = !panelOpen.value
  localStorage.setItem(PANEL_KEY, panelOpen.value ? '1' : '0')
}

// 点文件行 → 右侧文件详情面板（逐次 diff + 撤销入口都在面板里）
function openDetail(path: string) {
  store.openFileDetail(path)
}

// 撤回本轮（第 6 批）：按轮次检查点恢复最近一轮改过的文件。危险动作先确认；
// 文件在本轮之后被手工改过的会被跳过（不覆盖用户改动），结果与跳过原因都可见。
const { push: toast } = useToast()
const dialogs = useDialogs()
const reverting = ref(false)
async function revertRound() {
  if (reverting.value) return
  const ok = await dialogs.confirm({
    title: '撤回本轮',
    message: '按轮次检查点把最近一轮改过的文件恢复到本轮开始前？文件在本轮之后被手工改过的会被跳过（不覆盖你的改动）。',
    confirmText: '撤回',
    danger: true,
  })
  if (!ok) return
  reverting.value = true
  try {
    const res = await store.revertRound()
    const parts = [`已恢复 ${res.restored.length} 个文件`]
    if (res.skipped.length) parts.push(`撤不回 ${res.skipped.length} 个：${res.skipped.join('；')}`)
    else if (res.restored.length === 0) parts.push('（最近一轮没有文件改动，或已撤回）')
    toast(res.skipped.length ? 'error' : 'info', parts.join('；'))
  } catch (e) {
    toast('error', errText(e))
  } finally {
    reverting.value = false
  }
}
</script>

<template>
  <div
    v-if="groups.length"
    class="mx-1 mb-1.5 rounded-xl border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-2 py-1.5"
    aria-label="本轮变更"
  >
    <!-- 标题行即折叠开关（右端：撤回本轮——第 6 批） -->
    <div class="flex items-center gap-1">
    <button
      class="flex flex-1 items-center gap-1.5 rounded-lg px-1 py-0.5 text-[11px] text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-text)]"
      :aria-expanded="panelOpen"
      @click="togglePanel"
    >
      <AppIcon name="pencil" :size="11" />
      <span>本轮变更（{{ groups.length }} 个文件 · {{ changes.length }} 处）</span>
      <span class="font-mono">
        <span class="text-[var(--c-ok-text)]">+{{ totalAdd }}</span>
        <span class="ml-1 text-[var(--c-err-text)]">-{{ totalDel }}</span>
      </span>
      <AppIcon
        name="chevron-down"
        :size="12"
        class="ml-auto shrink-0 transition-transform"
        :class="panelOpen ? '' : '-rotate-90'"
      />
    </button>
    <button
      class="shrink-0 rounded-lg px-1.5 py-0.5 text-[10px] text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-err-text)]"
      :disabled="reverting"
      title="撤回本轮：按轮次检查点恢复最近一轮改过的文件（撤不回的会明确列出）"
      @click="revertRound"
    >
      {{ reverting ? '撤回中…' : '撤回本轮' }}
    </button>
    </div>

    <div v-if="panelOpen" class="mt-1 max-h-52 space-y-0.5 overflow-y-auto">
      <button
        v-for="g in groups"
        :key="g.path"
        class="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-xs transition-colors hover:bg-[var(--c-surface)]"
        :title="`${g.path} · 点击查看变更详情`"
        @click="openDetail(g.path)"
      >
        <span class="h-1.5 w-1.5 shrink-0 rounded-full bg-[var(--c-ok)]"></span>
        <span class="flex min-w-0 items-center">
          <span v-if="g.dir" class="min-w-0 truncate text-[var(--c-text-faint)]">{{ g.dir }}</span>
          <span class="shrink-0 font-medium text-[var(--c-text)]">{{ g.base }}</span>
        </span>
        <span
          class="shrink-0 rounded bg-[var(--c-primary-soft)] px-1.5 py-0.5 text-[10px] text-[var(--c-primary)]"
        >
          {{ g.count > 1 ? `改 ${g.count} 次` : '修改' }}
        </span>
        <span class="shrink-0 font-mono text-[11px]">
          <span class="text-[var(--c-ok-text)]">+{{ g.add }}</span>
          <span class="ml-1 text-[var(--c-err-text)]">-{{ g.del }}</span>
        </span>
        <AppIcon
          name="chevron-down"
          :size="12"
          class="ml-auto shrink-0 -rotate-90 text-[var(--c-text-faint)]"
        />
      </button>
    </div>
  </div>
</template>
