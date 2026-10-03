<script setup lang="ts">
import { computed } from 'vue'
import { useChatStore } from '../stores/chat'
import { useChannelStore } from '../stores/channels'
import { shortDir } from '../composables/workspaceLabel'
import AppIcon from './AppIcon.vue'

// 右栏「统计」tab（0.0.19）：按会话的 token 用量（后端账本 usage 事件聚合）。
// 排序：总量降序；纯展示不做任何写操作。旧账本没有 usage 事件（0.0.19 前的轮次
// 不计入）——空态与脚注照实写明，绝不编造"历史用量"。
const store = useChatStore()
const channels = useChannelStore()

interface Row {
  id: string
  title: string
  workspace: string
  prompt: number
  completion: number
  total: number
  prompt7d: number
  completion7d: number
  total7d: number
}

const rows = computed<Row[]>(() =>
  store.summaries
    .filter((s) => (s.totalTokens ?? 0) > 0)
    .sort((a, b) => (b.totalTokens ?? 0) - (a.totalTokens ?? 0))
    .map((s) => ({
      id: s.id,
      title: s.title || s.id,
      workspace: s.workspace ? shortDir(s.workspace) : '',
      prompt: s.promptTokens ?? 0,
      completion: s.completionTokens ?? 0,
      total: s.totalTokens ?? 0,
      prompt7d: s.prompt7d ?? 0,
      completion7d: s.completion7d ?? 0,
      total7d: s.total7d ?? 0,
    })),
)

const totalAll = computed(() => rows.value.reduce((acc, r) => acc + r.total, 0))
const total7d = computed(() => rows.value.reduce((acc, r) => acc + r.total7d, 0))
// 近 7 天趋势条（纯文本条形）：以窗口内最大值为基准，三段式显示
const trend7d = computed(() => {
  const max = Math.max(...rows.value.map((r) => r.total7d), 1)
  return rows.value
    .filter((r) => r.total7d > 0)
    .sort((a, b) => b.total7d - a.total7d)
    .slice(0, 6)
    .map((r) => {
      const n = Math.max(1, Math.round((r.total7d / max) * 10))
      return { id: r.id, title: r.title, n, label: fmt(r.total7d) }
    })
})

// 成本估算（0.0.24）：按当前激活渠道的单价（每百万 token）。
// 多渠道混用是近似值——面板明示口径，绝不冒充精确账单。
const price = computed(() => {
  const c = channels.activeChannel
  if (!c || !(c.priceIn || c.priceOut)) return null
  return { in: c.priceIn ?? 0, out: c.priceOut ?? 0 }
})
const cost7d = computed(() => {
  if (!price.value) return null
  const p = rows.value.reduce((acc, r) => acc + r.prompt7d, 0)
  const c = rows.value.reduce((acc, r) => acc + r.completion7d, 0)
  return (p / 1e6) * price.value.in + (c / 1e6) * price.value.out
})

function fmt(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col" aria-label="用量统计">
    <div class="flex items-center gap-2 border-b border-[var(--c-border)] px-3 py-2 text-xs">
      <AppIcon name="stats" :size="14" class="shrink-0 text-[var(--c-text-faint)]" />
      <span class="text-[var(--c-text-dim)]">按会话的 token 用量</span>
      <span class="ml-auto tabular-nums text-[var(--c-text-faint)]" title="全部会话累计">
        共 {{ fmt(totalAll) }} tok
      </span>
    </div>

    <p v-if="!rows.length" class="px-4 py-6 text-xs text-[var(--c-text-faint)]">
      还没有用量记录——统计从 0.0.19 开始沉淀（此前的对话没有记用量），跑一轮对话后回来就能看到。
    </p>

    <div v-else class="min-h-0 flex-1 overflow-y-auto px-2 py-2 text-xs">
      <div
        v-for="r in rows"
        :key="r.id"
        class="mb-1 flex items-center gap-2 rounded-lg px-2 py-1.5"
        :class="r.id === store.sessionId ? 'bg-[var(--c-primary-soft)]' : ''"
      >
        <div class="min-w-0 flex-1">
          <p class="truncate text-[var(--c-text)]" :title="r.title">{{ r.title }}</p>
          <p v-if="r.workspace" class="truncate text-[10px] text-[var(--c-text-faint)]" :title="r.workspace">
            {{ r.workspace }}
          </p>
        </div>
        <span
          class="shrink-0 tabular-nums text-[var(--c-text-dim)]"
          :title="`输入 ${fmt(r.prompt)} · 输出 ${fmt(r.completion)}`"
        >
          {{ fmt(r.total) }} tok
        </span>
      </div>
    </div>

    <!-- 近 7 天趋势 + 成本（0.0.24）：窗口内逐会话条形，条长以最大值为基准 -->
    <div v-if="rows.length" class="border-t border-[var(--c-border)] px-3 py-2">
      <p class="mb-1 flex items-center gap-2 text-[11px] text-[var(--c-text-dim)]">
        近 7 天
        <span class="tabular-nums text-[var(--c-text-faint)]">{{ fmt(total7d) }} tok</span>
        <span
          v-if="cost7d !== null"
          class="ml-auto tabular-nums text-[var(--c-text-dim)]"
          title="按当前激活渠道单价估算（每百万 token）；多渠道混用时为近似值"
        >
          ≈ ${{ cost7d.toFixed(2) }}
        </span>
      </p>
      <div v-for="t in trend7d" :key="t.id" class="flex items-center gap-2 py-0.5 text-[10px]">
        <span class="min-w-0 flex-1 truncate text-[var(--c-text-faint)]" :title="t.title">{{ t.title }}</span>
        <span class="shrink-0 font-mono text-[var(--c-primary)] opacity-70" aria-hidden="true">{{ '▮'.repeat(t.n) }}</span>
        <span class="w-14 shrink-0 text-right tabular-nums text-[var(--c-text-faint)]">{{ t.label }}</span>
      </div>
      <p class="mt-1 text-[9px] text-[var(--c-text-faint)]">
        仅统计 0.0.24 起带时间戳的轮次；成本按当前激活渠道单价估算，多渠道混用时为近似值。
      </p>
    </div>

    <p v-if="rows.length" class="border-t border-[var(--c-border)] px-3 py-2 text-[10px] text-[var(--c-text-faint)]">
      统计自 0.0.19 起沉淀（此前的轮次没有记录）；总量 = 输入 + 输出，悬浮看分项。
    </p>
  </section>
</template>
