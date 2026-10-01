<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useChatStore } from '../stores/chat'
import { errText } from '../composables/errText'
import { bridge } from '../wails'
import AppIcon from './AppIcon.vue'

// 浏览器驾驶舱（0.0.28）：右栏实时"看见"模型正在看的页面——只读 URL 栏 +
// 最新截图 + 控制台尾部 + 元素快照（折叠）。只读不操控：操控走对话里的 browser 工具。
// 数据源 = store.browserVisual（当前会话缓冲的派生）：新工具事件改写缓冲，这里
// watch 到 shot 变化自动重取——"跟随最新"不需要任何手动刷新，切会话也自动跟随。

const store = useChatStore()
const visual = computed(() => store.browserVisual)

// 截图读取状态机：idle（无图）/ loading / ok（dataUrl）/ error（可见错误 + 重试）。
// 竞态守卫：慢请求返回时只认最新一次（跟随最新连续重取时旧结果不得覆盖新画面）。
type ShotState =
  | { kind: 'idle' | 'loading'; dataUrl: string; error: string }
  | { kind: 'ok'; dataUrl: string; error: string }
  | { kind: 'error'; dataUrl: string; error: string }
const shot = ref<ShotState>({ kind: 'idle', dataUrl: '', error: '' })
let fetchSeq = 0

// 扩展名定 MIME（后端契约：默认 .png，超限降级 .jpg）——不猜，认路径
function shotMime(path: string): string {
  return path.toLowerCase().endsWith('.jpg') ? 'image/jpeg' : 'image/png'
}

async function loadShot(path: string) {
  if (!path) {
    shot.value = { kind: 'idle', dataUrl: '', error: '' }
    return
  }
  const seq = ++fetchSeq
  shot.value = { kind: 'loading', dataUrl: '', error: '' }
  try {
    const b64 = await bridge().app.ReadBrowserShot(path)
    if (seq !== fetchSeq) return // 已有更新的请求在途：过期结果丢弃
    if (!b64) throw new Error('截图内容为空') // 空串拼 data URL 是坏图，按错误态可见
    shot.value = { kind: 'ok', dataUrl: `data:${shotMime(path)};base64,${b64}`, error: '' }
  } catch (e) {
    if (seq !== fetchSeq) return
    shot.value = { kind: 'error', dataUrl: '', error: errText(e) }
  }
}
watch(
  () => visual.value.shot,
  (p) => void loadShot(p),
  { immediate: true },
)

// 控制台 error 级行着色（行内级别前缀是内核的产出格式，见 browsertool 的日志方案）
function consoleLineClass(line: string): string {
  return /\[(error|页面错误)\]/.test(line) ? 'text-[var(--c-err-text)]' : 'text-[var(--c-text-dim)]'
}

// 元素快照折叠态：默认收起（画面才是主体），手动展开后保持
const snapshotOpen = ref(false)
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col" aria-label="浏览器驾驶舱">
    <!-- 头部：只读 URL 栏 + 执行中指示 + 关闭（Esc） -->
    <div class="flex items-center gap-2 border-b border-[var(--c-border)] px-3 py-2.5">
      <AppIcon name="image" :size="14" class="shrink-0 text-[var(--c-text-faint)]" />
      <div
        class="min-w-0 flex-1 truncate font-mono text-xs"
        :class="visual.url ? 'text-[var(--c-text)]' : 'text-[var(--c-text-faint)]'"
        :title="visual.url"
        aria-label="当前页面地址"
      >
        {{ visual.url || '尚未打开页面' }}
      </div>
      <span v-if="visual.running" class="flex shrink-0 items-center gap-1 text-[11px] text-[var(--c-warn-text)]">
        <span class="h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--c-warn)]"></span>
        执行中
      </span>
      <button class="btn-icon" title="关闭（Esc）" @click="store.closeBrowserPanel()">
        <AppIcon name="x" :size="14" />
      </button>
    </div>

    <div class="min-h-0 flex-1 space-y-3 overflow-y-auto px-3 pb-3">
      <!-- 最新截图（跟随最新）：加载失败必须有可见错误态，绝不拿空白冒充画面 -->
      <div class="flex flex-col gap-1 pt-1">
        <div class="flex items-center gap-2 text-[11px] text-[var(--c-text-faint)]">
          <span class="stat px-1.5 py-0.5">画面（跟随最新）</span>
          <span v-if="shot.kind === 'loading'" class="text-[var(--c-text-faint)]">读取中…</span>
          <span v-if="shot.kind === 'error'" class="ml-auto">读取失败</span>
        </div>
        <img
          v-if="shot.kind === 'ok'"
          :src="shot.dataUrl"
          alt="浏览器最新截图"
          class="w-full rounded-lg border border-[var(--c-border)]"
        />
        <div
          v-else-if="shot.kind === 'error'"
          class="flex flex-col items-start gap-1.5 rounded-lg border border-[var(--c-err)] bg-[var(--c-err-soft)] px-2.5 py-2"
          role="alert"
        >
          <span class="flex items-center gap-1.5 text-xs text-[var(--c-err-text)]">
            <AppIcon name="alert" :size="13" />
            {{ shot.error }}
          </span>
          <button class="chip text-[11px]" title="重新读取这张截图" @click="loadShot(visual.shot)">重试</button>
        </div>
        <div
          v-else
          class="flex min-h-24 items-center justify-center rounded-lg border border-dashed border-[var(--c-border)] px-3 py-6 text-center text-xs text-[var(--c-text-faint)]"
        >
          暂无截图——browser 动作完成后这里显示模型正在看的页面
        </div>
      </div>

      <!-- 控制台尾部（≤8 条）：报错行着警示色，模型的"看见"这里复述 -->
      <div v-if="visual.console.length" class="flex flex-col gap-1">
        <div class="text-[11px] text-[var(--c-text-faint)]">控制台（最近 {{ visual.console.length }} 条）</div>
        <div class="rounded-lg border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-2.5 py-1.5 font-mono text-[11px] leading-5">
          <div v-for="(l, i) in visual.console" :key="i" class="whitespace-pre-wrap break-all" :class="consoleLineClass(l)">
            {{ l }}
          </div>
        </div>
      </div>

      <!-- 元素快照（可折叠）：最近一次 open/snapshot/scroll 的 [ref] 元素列表 -->
      <div v-if="visual.snapshot" class="flex flex-col gap-1">
        <button
          class="chip self-start text-[11px]"
          :aria-expanded="snapshotOpen"
          title="展开/收起模型看到的可交互元素列表"
          @click="snapshotOpen = !snapshotOpen"
        >
          <AppIcon name="chevron-down" :size="11" :class="snapshotOpen ? '' : '-rotate-90'" class="transition-transform" />
          元素快照
        </button>
        <pre v-if="snapshotOpen" class="tool-full">{{ visual.snapshot }}</pre>
      </div>
    </div>
  </section>
</template>
