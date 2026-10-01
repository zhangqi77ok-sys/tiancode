<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useChatStore } from '../stores/chat'
import { errText } from '../composables/errText'
import { bridge, type BgTaskDTO } from '../wails'
import AppIcon from './AppIcon.vue'

// 右栏「任务」tab：本会话 shell bg_start 的后台任务（id/命令/状态/日志）。
// 任务真相在 shell 工具内存里（不落账本、没有推送事件），面板打开期间低频轮询
// 快照（2s），关 tab 即停；会话切换立即重取（快照按会话隔离，不串台）。
// 宽度/边框/Esc 归 RightPanel 容器，本组件只管内容。

const store = useChatStore()

const POLL_MS = 2000
const tasks = ref<BgTaskDTO[]>([])
const loading = ref(false)
const error = ref('')
let timer: number | null = null
// 轮询代际：会话切换重取后，旧会话的慢响应返回时整体丢弃——
// 不加守卫的话旧会话的任务快照会覆盖当前面板（与 FileTreePanel 同款纪律）
let gen = 0

async function refresh() {
  gen++
  const g = gen
  loading.value = true
  error.value = ''
  try {
    const snap = (await bridge().app.BgTasksSnapshot(store.sessionId)) ?? []
    if (g !== gen) return // 会话已切换：过期结果丢弃
    tasks.value = snap
  } catch (e) {
    if (g !== gen) return
    error.value = errText(e) // 失败可见：空列表与"没有任务"必须可区分
    tasks.value = []
  } finally {
    if (g === gen) loading.value = false
  }
}

// 状态文案：运行中 / 正常结束 / 退出码 N——退出码 -1（被强杀/未知）照实显示
function statusText(t: BgTaskDTO): string {
  if (t.running) return '运行中'
  return t.exitCode === 0 ? '正常结束' : `退出码 ${t.exitCode}`
}

function statusClass(t: BgTaskDTO): string {
  if (t.running) return 'bg-[var(--c-primary)]'
  return t.exitCode === 0 ? 'bg-[var(--c-ok)]' : 'bg-[var(--c-err)]'
}

const runningCount = computed(() => tasks.value.filter((t) => t.running).length)

// 会话切换：立即重取（轮询下一跳自动跟随新会话）
watch(
  () => store.sessionId,
  () => void refresh(),
)

onMounted(() => {
  void refresh()
  timer = window.setInterval(() => void refresh(), POLL_MS)
})

onBeforeUnmount(() => {
  if (timer !== null) window.clearInterval(timer) // 关 tab 即停：面板不在场时不空转
})
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col" aria-label="后台任务">
    <!-- 头部：标题 + 刷新 + 关闭（Esc 经容器消费栈，这里给显式按钮） -->
    <div class="flex items-center gap-2 border-b border-[var(--c-border)] px-3 py-2.5">
      <AppIcon name="terminal" :size="14" class="shrink-0 text-[var(--c-text-faint)]" />
      <div class="min-w-0 flex-1 truncate text-sm font-medium">后台任务</div>
      <span v-if="runningCount" class="flex shrink-0 items-center gap-1 text-[11px] text-[var(--c-primary)]">
        <span class="h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--c-primary)]"></span>
        {{ runningCount }} 个运行中
      </span>
      <button class="btn-icon" title="立即刷新" @click="refresh">
        <AppIcon name="refresh" :size="14" />
      </button>
      <button class="btn-icon" title="关闭（Esc）" @click="store.closeTasksPanel()">
        <AppIcon name="x" :size="14" />
      </button>
    </div>

    <div class="min-h-0 flex-1 space-y-2 overflow-y-auto px-3 py-2.5">
      <!-- 读取失败：可见 + 重试，不用空白冒充"没有任务" -->
      <div
        v-if="error"
        class="flex flex-col items-start gap-1.5 rounded-lg border border-[var(--c-err)] bg-[var(--c-err-soft)] px-2.5 py-2"
        role="alert"
      >
        <span class="flex items-center gap-1.5 text-xs text-[var(--c-err-text)]">
          <AppIcon name="alert" :size="13" />
          {{ error }}
        </span>
        <button class="chip text-[11px]" title="重新读取后台任务" @click="refresh">重试</button>
      </div>
      <div v-else-if="loading && !tasks.length" class="px-2 py-6 text-center text-xs text-[var(--c-text-faint)]">读取中…</div>
      <div v-else-if="!tasks.length" class="px-2 py-6 text-center text-xs text-[var(--c-text-faint)]">
        本会话还没有后台任务——模型用 shell 的 bg_start 启动的命令会出现在这里
      </div>

      <div
        v-for="t in tasks"
        :key="t.id"
        class="rounded-lg border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-2.5 py-2"
      >
        <div class="flex items-center gap-2 text-xs">
          <span class="h-1.5 w-1.5 shrink-0 rounded-full" :class="statusClass(t)" :title="statusText(t)"></span>
          <span class="shrink-0 font-mono">{{ t.id }}</span>
          <span
            class="shrink-0"
            :class="t.running ? 'text-[var(--c-primary)]' : t.exitCode === 0 ? 'text-[var(--c-ok-text)]' : 'text-[var(--c-err-text)]'"
          >{{ statusText(t) }}</span>
          <span
            class="ml-auto shrink-0 text-[10px] text-[var(--c-text-faint)]"
            :title="`PID ${t.pid} · 启动于 ${new Date(t.startedAt).toLocaleString()}`"
          >PID {{ t.pid }}</span>
        </div>
        <div class="mt-1 break-all font-mono text-[11px] leading-5 text-[var(--c-text)]">{{ t.command }}</div>
        <pre
          v-if="t.log"
          class="mt-1.5 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-md border border-[var(--c-border)] bg-[var(--c-surface)] px-2 py-1.5 font-mono text-[11px] leading-5 text-[var(--c-text-dim)]"
          aria-label="任务日志"
        >{{ t.log }}</pre>
      </div>
    </div>
  </section>
</template>
