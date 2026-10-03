<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { ChatMsg } from '../stores/chat'
import { useChatStore } from '../stores/chat'
import { diffLineClass, diffStat } from '../composables/diffView'
import { errText } from '../composables/errText'
import { openPathAt } from '../composables/openPath'
import { parseOutputRows, type OutputRow } from '../composables/outputRows'
import { useClipboard } from '../composables/useClipboard'
import { useToast } from '../composables/useToast'
import { bridge } from '../wails'
import AppIcon from './AppIcon.vue'

// 工具卡 = 全宽动作行（对齐商用编码智能体的会话流）：
// [状态点] [类型图标] [主标签] [动作徽章] [+N -M] …………… [查看变更/详情 ▾]
// 主标签来自内核 Title（文件名/命令首段/搜索词——最清楚自己干了谁的一方生产语义，
// ADR-0006 同纪律：结构化字段优于 UI 解析文本）；旧账本无 Title → 回退工具名 + 内容片段。
const props = defineProps<{ m: ChatMsg }>()

const { push: toast } = useToast()
const { copy } = useClipboard()

// 折叠态语义（0.0.06）：
//   - 执行中：**强制展开**且不允许折叠——输出正在生长；
//   - write/edit（有 diff）：默认展开 diff——变更就是这张卡的主体，不能藏；
//   - 其余：默认折叠，用户手动开合后以手动为准。
const userToggled = ref(false)
const open = ref(false)
const isRunning = computed(() => props.m.status === 'running')
const expandable = computed(() => isRunning.value || !!(props.m.diff && (props.m.op === 'write' || props.m.op === 'edit')))
const effectiveOpen = computed(() => (isRunning.value ? true : userToggled.value ? open.value : expandable.value))
function toggle() {
  if (isRunning.value) return // 执行中不允许折叠（结束后再收）
  userToggled.value = true
  open.value = !open.value
}

// 主标签：内核 Title 优先；旧数据回退"工具名 · 内容片段"
const label = computed(() => {
  const t = props.m.title?.trim()
  if (t) return t
  const name = props.m.toolName || '工具'
  const s = (props.m.content || '').replace(/\s+/g, ' ').trim()
  return s ? `${name} · ${s}`.slice(0, 80) : name
})

// 路径两段式（第 2 批）：目录（暗色、可截断）+ 末段（亮色、不缩）——多目录同名
// 文件（agent.go）一眼可区分。仅文件类操作且有层级时拆分；旧账本的末段标签
// 天然单段，走通用分支。
const pathParts = computed<{ dir: string; base: string } | null>(() => {
  const t = props.m.title?.trim() || ''
  if (!['read', 'write', 'edit', 'list', 'tree'].includes(props.m.op ?? '')) return null
  const i = t.lastIndexOf('/')
  if (i <= 0 || i === t.length - 1) return null
  return { dir: t.slice(0, i + 1), base: t.slice(i + 1) }
})

// 动作徽章：内核 Op → 中文
const OP_LABEL: Record<string, string> = {
  read: '读取',
  list: '浏览',
  tree: '结构',
  write: '写入',
  edit: '修改',
  exec: '执行',
  search: '搜索',
  git: '版本',
}
const opLabel = computed(() => (props.m.op ? OP_LABEL[props.m.op] : ''))

// 图标按动作/工具选型：命令 → 终端；搜索 → 放大镜；写改 → 文件；git → 分支环；其余 → 扳手
const iconName = computed(() => {
  if (props.m.op === 'exec' || props.m.toolName === 'shell') return 'terminal'
  if (props.m.op === 'search' || props.m.toolName === 'search') return 'search'
  if (props.m.op === 'write' || props.m.op === 'edit' || props.m.diff) return 'file'
  if (props.m.op === 'git' || props.m.toolName === 'git') return 'refresh'
  return 'wrench'
})

// "在资源管理器中显示"（0.0.06）：只对文件类动作出按钮——主标签即工作区相对路径。
// shell/git/search 的主标签是命令/搜索词，没有对应的文件位置。
const revealable = computed(() => ['read', 'write', 'edit', 'list', 'tree'].includes(props.m.op ?? ''))

// 文件详情（第 3 批）：成功 write/edit 卡的文件名可点——右侧面板是 diff 主视图
const reviewable = computed(
  () => props.m.status === 'success' && (props.m.op === 'write' || props.m.op === 'edit') && !!props.m.title?.trim(),
)
async function reveal() {
  try {
    // 0.0.11：路径按这场对话的工作区解析（传当前会话 ID）
    await bridge().app.RevealInExplorer(store.sessionId, props.m.title?.trim() || '')
  } catch (e) {
    toast('error', errText(e))
  }
}

// "恢复写入前"（0.0.07）：write/edit 成功且带撤销快照时出现。走后端恢复
// （旧全文只在账本里，比对哈希防覆盖用户改动）；成功后 store 追加"已恢复"卡。
const store = useChatStore()
const restoring = ref(false)
async function restore() {
  if (!props.m.callId || restoring.value) return
  restoring.value = true
  await store.restoreWrite(props.m.callId, props.m.undoPath)
  restoring.value = false
}

// diffstat 与行着色（与审查带/详情面板同一来源：composables/diffView）
const stat = computed(() => (props.m.diff ? diffStat(props.m.diff) : null))

// 复制输出（第 7 批）：复制这张卡当前展示的全文——有 diff 给 diff，否则给原始输出
//（搜索结果、命令输出都是纯文本）。失败同样 toast，不假装成功。
const copyPayload = computed(() => (props.m.diff ? props.m.diff : (props.m.content ?? '')))
async function copyOutput() {
  const text = copyPayload.value
  if (!text) return
  await copy(text, { success: '已复制输出' })
}

// 打开文件（0.0.12；第 8 批加行号）：走统一入口 openPathAt——配了「在这一行打开」
// 就用那条命令，没配则与 OpenInDefaultApp 完全一致。失败走 toast，绝不静默。
async function openPath(path: string, line = 0) {
  await openPathAt(store.sessionId, path, line, (m) => toast('error', m))
}

// 输出行（0.0.12 搜索结果 / 第 8 批 shell 等）：解析规则见 composables/outputRows.ts
// （纯函数 + 单测）。第 8 批起，**其它非 diff 输出**（go test / go build 的报错行）里
// 出现 `path:line:` / `path:line:col:` 时路径同样可点——此前那是一整块死文本。
const resultRows = computed<OutputRow[] | null>(() => {
  if (props.m.diff || props.m.status === 'running') return null
  return parseOutputRows(props.m.content || '', {
    isSearch: props.m.op === 'search' || props.m.toolName === 'search',
    searchOK: props.m.status === 'success',
  })
})

// 折叠态摘要用：输出里有多少行带可点的 path:line（0.0.26"测试失败结构化"的
// 轻量形态——不新造面板，卡片自己报数；要看具体行仍点展开）。
const locatableCount = computed(() => (resultRows.value ?? []).filter((r) => r.openable && r.line > 0).length)

// 截图内嵌（0.0.30 用户反馈）：browser 终态卡带 shot 时**直接展示图片本体**——
// 此前消息流里只有"已截图 路径"一行字，图只在右侧驾驶舱，用户点开对话才看到
// 一条路径。MIME 规则与 BrowserPanel 同源（后端只产 png/jpg 两态）。
const shotCache = new Map<string, string>() // 模块级：同一路径跨卡片、跨重放只读一次盘
const shotUrl = ref('')
const shotErr = ref('')
const lightbox = ref('') // 点击放大（与 MessageBubble 的附件放大同款全屏浮层）
function shotMime(p: string): string {
  return p.toLowerCase().endsWith('.jpg') || p.toLowerCase().endsWith('.jpeg') ? 'image/jpeg' : 'image/png'
}
watch(
  () => props.m.shot,
  async (p) => {
    shotUrl.value = ''
    shotErr.value = ''
    if (props.m.toolName !== 'browser' || !p) return
    const hit = shotCache.get(p)
    if (hit) {
      shotUrl.value = hit
      return
    }
    try {
      const b64 = await bridge().app.ReadBrowserShot(p)
      if (!b64) throw new Error('截图内容为空')
      const url = `data:${shotMime(p)};base64,${b64}`
      shotCache.set(p, url)
      if (props.m.shot === p) shotUrl.value = url // 途中 shot 已更新：过期结果丢弃
    } catch (e) {
      if (props.m.shot === p) shotErr.value = errText(e) // 失败可见，不静默吞图
    }
  },
  { immediate: true },
)
</script>

<template>
  <div class="flex w-full max-w-[94%] flex-col gap-1">
    <button
      class="flex w-full items-center gap-2 rounded-xl border px-3 py-2 text-xs transition-colors"
      :class="
        m.status === 'error'
          ? 'border-[var(--c-err)] bg-[var(--c-err-soft)]'
          : 'border-[var(--c-border)] bg-[var(--c-surface-soft)] hover:border-[var(--c-primary)]'
      "
      :aria-expanded="effectiveOpen"
      @click="toggle"
    >
      <span
        class="h-1.5 w-1.5 shrink-0 rounded-full"
        :class="m.status === 'error' ? 'bg-[var(--c-err)]' : isRunning ? 'animate-pulse bg-[var(--c-warn)]' : 'bg-[var(--c-ok)]'"
      ></span>
      <AppIcon :name="iconName" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
      <!-- 文件主标签（0.0.09 / 第 2 批两段式）：目录暗色可截断 + 末段亮色不缩——
           多目录同名文件一眼可区分；write/edit 点击末段 → 右侧文件详情面板 -->
      <span
        class="flex min-w-0 items-center font-medium text-[var(--c-text)]"
        :class="reviewable ? 'cursor-pointer' : ''"
        :title="reviewable ? `${label} · 点击在右侧查看变更详情` : m.content"
        @click.stop="reviewable ? store.openFileDetail(m.title || '') : undefined"
      >
        <span v-if="pathParts" class="min-w-0 truncate text-[var(--c-text-faint)]">{{ pathParts.dir }}</span>
        <span
          class="shrink-0"
          :class="reviewable ? 'underline decoration-dotted underline-offset-2' : ''"
          >{{ pathParts?.base ?? label }}</span
        >
      </span>
      <span
        v-if="opLabel"
        class="shrink-0 rounded-md bg-[var(--c-primary-soft)] px-1.5 py-0.5 text-[10px] text-[var(--c-primary)]"
      >
        {{ opLabel }}
      </span>
      <span v-if="stat" class="shrink-0 font-mono text-[11px]">
        <span class="text-[var(--c-ok-text)]">+{{ stat.add }}</span>
        <span class="ml-1 text-[var(--c-err-text)]">-{{ stat.del }}</span>
      </span>
      <span class="ml-auto shrink-0 pl-2 text-[var(--c-text-faint)]">
        {{ isRunning ? '执行中' : m.diff ? '查看变更' : '详情' }}
      </span>
      <AppIcon
        name="chevron-down"
        :size="12"
        class="shrink-0 text-[var(--c-text-faint)] transition-transform"
        :class="effectiveOpen ? '' : '-rotate-90'"
      />
    </button>

    <!-- 失败聚合摘要（0.0.26）：模型跑的 go test / go build 失败时，卡片折叠着也能
         看到"有几处可点位置"——不必逐卡展开翻输出（CheckResults 面板只覆盖工作区
         检查命令那条路径，模型自己跑的测试不走它）。 -->
    <p
      v-if="!effectiveOpen && !m.diff && !isRunning && locatableCount > 0"
      class="ml-1 mt-0.5 text-[11px] text-[var(--c-text-dim)]"
    >
      {{ locatableCount }} 处可点位置（展开可直接跳到出错行）
    </p>

    <!-- 截图内嵌（0.0.30）：browser 卡的可视结果直接展示在消息流里——截图是动作
         本体，不进折叠、不看开合状态；读取失败按错误态可见（绝不静默吞图） -->
    <div v-if="shotUrl || shotErr" class="max-w-[92%]">
      <img
        v-if="shotUrl"
        :src="shotUrl"
        alt="浏览器截图"
        class="w-full cursor-zoom-in rounded-xl border border-[var(--c-border)]"
        title="点击放大预览"
        @click.stop="lightbox = shotUrl"
      />
      <p
        v-else
        class="rounded-xl border border-[var(--c-err)] bg-[var(--c-err-soft)] px-3 py-2 text-[11px] text-[var(--c-err-text)]"
      >
        截图读取失败：{{ shotErr }}
      </p>
    </div>

    <!-- 有 diff：变更面板即展开主体（write/edit 默认展开，不再用矮容器把变更藏住）；
         无 diff：展开全文（命令卡看完整输出，只读信息不丢） -->
    <div
      v-if="effectiveOpen && m.diff"
      class="max-h-[60vh] max-w-[92%] overflow-auto whitespace-pre rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] px-3 py-2 font-mono text-xs leading-5"
    >
      <div v-for="(l, li) in m.diff.split('\n')" :key="li" :class="diffLineClass(l)">{{ l }}</div>
    </div>
    <!-- 搜索结果（0.0.12）：命中行 = path:line:（浅底、路径可点开文件）；上下文行 =
         path-line-（暗色）；files_only 的路径列表整行可点。行号不跳转——只打开文件。 -->
    <div v-else-if="effectiveOpen && resultRows" class="tool-full space-y-0.5">
      <div
        v-for="r in resultRows"
        :key="r.key"
        class="flex items-baseline"
        :class="r.hit ? 'rounded bg-[var(--c-primary-soft)]' : r.sep === '-' ? 'text-[var(--c-text-dim)]' : ''"
      >
        <button
          v-if="r.openable && r.prefix"
          class="shrink-0 cursor-pointer underline decoration-dotted underline-offset-2 hover:text-[var(--c-primary)]"
          :title="
            r.line
              ? `打开 ${r.prefix}（配了「在这一行打开」时定位到第 ${r.line} 行）`
              : `打开 ${r.prefix}（用系统默认程序，不定位到行）`
          "
          @click.stop="openPath(r.prefix, r.line)"
        >
          {{ r.prefix }}
        </button>
        <span v-else-if="r.prefix" class="shrink-0">{{ r.prefix }}</span>
        <span v-if="r.sep" class="shrink-0 opacity-70">{{ r.sep }}{{ r.lineNo }}{{ r.sep }}</span>
        <span class="min-w-0 truncate">{{ r.text }}</span>
      </div>
    </div>
    <pre v-else-if="effectiveOpen" class="tool-full">{{ m.content }}</pre>

    <!-- 撤销（0.0.07）：不可恢复说明优先显示（如超大文件未保存快照）；
         有快照时提供"恢复写入前"——走后端比对，文件被改过会被拒绝并说明 -->
    <p v-if="m.undoNote" class="self-start px-1 text-[11px] text-[var(--c-text-faint)]">{{ m.undoNote }}</p>
    <button
      v-if="m.hasUndo && m.callId && effectiveOpen"
      class="chip self-start text-[11px]"
      :disabled="restoring"
      title="把文件写回这次修改之前的内容（文件后来被改过时会被拒绝）"
      @click="restore"
    >
      <AppIcon name="refresh" :size="11" /> {{ restoring ? '正在恢复…' : '恢复写入前' }}
    </button>

    <div v-if="(revealable || copyPayload) && effectiveOpen" class="flex gap-1.5 self-start">
      <button v-if="revealable" class="chip text-[11px]" title="打开文件所在目录并选中" @click="reveal">
        <AppIcon name="file" :size="11" /> 在资源管理器中显示
      </button>
      <!-- 打开文件（0.0.12）：read/list 等卡片的主标签就是路径，可直接用系统默认程序打开 -->
      <button
        v-if="revealable"
        class="chip text-[11px]"
        title="用系统默认程序打开（不定位到行）"
        @click="openPath(m.title || '')"
      >
        <AppIcon name="external" :size="11" /> 打开
      </button>
      <!-- 复制输出（第 7 批）：卡片全文（含搜索结果与命令输出）-->
      <button v-if="copyPayload" class="chip text-[11px]" title="复制这张卡的输出全文" @click="copyOutput">
        <AppIcon name="copy" :size="11" /> 复制输出
      </button>
    </div>

    <!-- 放大预览（与 MessageBubble 附件放大同款全屏浮层）：点外面或再点图片关闭 -->
    <Teleport to="body">
      <div
        v-if="lightbox"
        class="fixed inset-0 z-50 flex cursor-zoom-out items-center justify-center bg-black/70 p-6"
        @click="lightbox = ''"
      >
        <img :src="lightbox" class="max-h-full max-w-full rounded-lg" alt="放大截图" />
      </div>
    </Teleport>
  </div>
</template>
