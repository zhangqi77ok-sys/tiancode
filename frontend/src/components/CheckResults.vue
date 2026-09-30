<script setup lang="ts">
import { computed, ref } from 'vue'
import { useChatStore } from '../stores/chat'
import { openPathAt } from '../composables/openPath'
import { useToast } from '../composables/useToast'
import type { CheckRefDTO, CheckResultDTO } from '../wails'
import AppIcon from './AppIcon.vue'

// 工作区检查结果（第 8 批）：回合结束后自动跑的命令，把输出里的 path:line 收成可点列表。
// 只读展示：命令跑不跑由 workspace-settings.json 决定（空 = 从不跑），这里不提供按钮。
const props = defineProps<{ result: CheckResultDTO | null }>()
const store = useChatStore()
const { push: toast } = useToast()
const open = ref(true)

const refs = computed<CheckRefDTO[]>(() => props.result?.refs ?? [])
const summary = computed(() => {
  const r = props.result
  if (!r) return ''
  const bits = [`${refs.value.length} 处位置`]
  if (r.timedOut) bits.push('超时终止')
  else if (r.failed) bits.push('命令非零退出')
  return bits.join(' · ')
})

async function openRef(r: CheckRefDTO) {
  await openPathAt(store.sessionId, r.path, r.line, (m) => toast('error', m))
}
</script>

<template>
  <div v-if="result" class="mx-1 mb-1 rounded-xl border border-[var(--c-border)] bg-[var(--c-surface-soft)] text-xs">
    <button
      class="flex w-full items-center gap-2 px-3 py-1.5 text-[var(--c-text-dim)]"
      :aria-expanded="open"
      @click="open = !open"
    >
      <AppIcon name="terminal" :size="12" class="shrink-0 text-[var(--c-text-faint)]" />
      <span class="shrink-0 font-mono">{{ result.command }}</span>
      <span class="min-w-0 flex-1 truncate text-left text-[var(--c-text-faint)]">{{ summary }}</span>
      <AppIcon
        name="chevron-down"
        :size="12"
        class="shrink-0 transition-transform"
        :class="open ? '' : '-rotate-90'"
      />
    </button>
    <div v-if="open" class="space-y-0.5 px-3 pb-2">
      <p v-if="!refs.length" class="text-[11px] text-[var(--c-text-faint)]">没有可点的位置引用</p>
      <div v-for="(r, i) in refs" :key="`${r.path}:${r.line}:${i}`" class="flex items-baseline gap-1">
        <button
          class="shrink-0 cursor-pointer underline decoration-dotted underline-offset-2 hover:text-[var(--c-primary)]"
          :title="`打开 ${r.path}（配了「在这一行打开」时定位到第 ${r.line} 行）`"
          @click="openRef(r)"
        >
          {{ r.path }}:{{ r.line }}{{ r.col ? `:${r.col}` : '' }}
        </button>
        <span class="min-w-0 truncate text-[var(--c-text-faint)]">{{ r.text }}</span>
      </div>
    </div>
  </div>
</template>
