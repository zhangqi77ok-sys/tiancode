<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useChatStore } from '../stores/chat'
import { useChannelStore } from '../stores/channels'
import { useWorkspaceStore } from '../stores/workspace'
import { useEscClose } from '../composables/useEsc'
import { errText } from '../composables/errText'
import { useClipboard } from '../composables/useClipboard'
import { useToast } from '../composables/useToast'
import { THEME_LABEL, currentTheme, cycleTheme, type ThemeMode } from '../composables/useTheme'
import { useContextGauge } from '../composables/useContextGauge'
import { workspaceLabel } from '../composables/workspaceLabel'
import { bridge, winClose, winMinimize, winToggleMaximize } from '../wails'
import AppIcon from './AppIcon.vue'

// 顶栏：品牌 + 运行状态 + 导出/工作区/命令确认。
// 渠道/模型/技能/MCP 入口统一在侧栏底部（0.0.06）；汉堡导航已下线。
// 纯状态展示用 .stat（无 hover 态），可点操作用 .chip——不制造假可点。
defineProps<{ navOpen?: boolean }>()

const store = useChatStore()
const channels = useChannelStore()
const ws = useWorkspaceStore()
const { push: toast } = useToast()
const { copy } = useClipboard()

const approvalOn = ref(false)

// 顶栏工作区（0.0.07）：主标签 = **这场对话正在用的路**（已落账会话取账本归属；
// 草稿取下一场的根），不再是笼统的"当前工作区"——ws.path 只影响还没落账的新对话，
// 把它当"当前"会误导用户以为正在看的对话会写进那个目录。
// 归属与下一场根不同时，副标签用弱样式另标「新建对话将使用 …」（禁止都叫"当前"）。
const isDraft = computed(() => !store.sessionId)
const sessionWs = computed(() => store.summaries.find((s) => s.id === store.sessionId)?.workspace ?? '')
const wsLabel = computed(() =>
  workspaceLabel({ isDraft: isDraft.value, sessionWorkspace: sessionWs.value, draftWorkspace: ws.path }),
)

// 顶栏中段（阶段 1）：只显示本场对话工作区的**路径末段**（完整路径进 title）与分支；
// 两者都没有时明示"这场对话没有工作区"（弱样式，不冒充也不留空）。
// 顶栏中段（0.0.18 修正）：非草稿一律显示**这场对话的归属**——归属为空时明示
// "这场对话没有工作区"（wsLabel.main，此前算了没渲染），绝不拿"下一场新对话"
// 的根冒充当前对话的路；草稿显示下一场的根（它就是发出后的归属）。
const shownWorkspace = computed(() => (isDraft.value ? ws.path : sessionWs.value))
const wsFullPath = computed(() => shownWorkspace.value)
const wsBase = computed(() => {
  const p = shownWorkspace.value.replace(/[\\/]+$/, '')
  const i = Math.max(p.lastIndexOf('\\'), p.lastIndexOf('/'))
  return i >= 0 ? p.slice(i + 1) : p
})

// 当前分支（0.0.11）：已落账会话取**该会话的**工作区，草稿取"下一场新对话"的根。
// 没有工作区 / 不是 git 仓库 / 命令失败 → 空串（不渲染，绝不编造分支名）；
// 顶栏也不因此报错——「看分支」失败不该弹与用户操作无关的错误框。
const branch = ref('')
async function loadBranch() {
  try {
    branch.value = (await bridge().app.CurrentBranch(store.sessionId)) || ''
  } catch {
    branch.value = ''
  }
}
// 切会话（归属换了）或换工作区（草稿的根换了）都要重取
watch([() => store.sessionId, () => ws.path], () => void loadBranch(), { immediate: true })

// 状态灯文案：后台运行 / 待答复都要与"空闲"区分（切走后顶栏不能装作没事）。
// 0.0.06：带上会话标题——多个会话并行时"后台运行中"说不清是谁在跑。
const busyTitle = computed(() => (store.busyTarget ? store.titleOf(store.busyTarget) || '未命名会话' : ''))
const statusText = computed(() => {
  if (store.anyPending) return `${store.anyPending} 项待确认 · ${busyTitle.value}`
  if (!store.anyRunning) return '空闲'
  return store.running ? '运行中' : `后台运行中 · ${busyTitle.value}`
})

// 状态点（阶段 1）：空闲只留一个点（文案进 title），忙时才补一小段——
// "有几项待确认"是不能只靠颜色表达的（色觉差异 + 一眼扫不到）。
const statusDotClass = computed(() =>
  store.anyPending
    ? 'bg-[var(--c-warn)]'
    : store.anyRunning
      ? 'animate-pulse bg-[var(--c-primary)]'
      : 'bg-[var(--c-text-faint)]',
)
const busyLabel = computed(() => {
  if (store.anyPending) return `${store.anyPending} 项待确认`
  return store.anyRunning ? '运行中' : ''
})
// 悬停看运行 / 渠道 / 模型：顶栏只留一个点，信息全进 title
const statusTitle = computed(() => {
  const chan = channels.activeChannel ? `渠道 ${channels.activeChannel.name}` : '未配置渠道'
  const model = channels.activeModel ? `模型 ${channels.activeModel}` : '未选择模型'
  return `${statusText.value} · ${chan} · ${model}`
})

// 油表（0.0.09；0.3 起读数与文案收敛到 useContextGauge，与 Composer 输入框同源）：
// prompt token 来自上游 usage 的直接读数；渠道声明了上下文上限（contextLimit）时
// 同时显示剩余比例；本轮发生过折叠时显式标注（折叠绝不静默）。没有上限时不编造
// 百分比（沿用 0.0.09 纪律）。
const { usage } = useContextGauge()

// 状态灯点击：跳到等待处理的会话（优先"待确认"，其次其他运行中会话）
function jumpToBusy() {
  if (store.busyTarget) void store.selectSession(store.busyTarget)
}

// ---- 顶栏图标菜单（阶段 1 瘦身）----
// 为什么收：导出 / 命令确认 / 主题 / 工作区四个 chip 加上油表长句，1280 宽会折成两行。
// 行为一个不减（导出仍是复制与另存、审批仍覆盖 shell 与 ext_manage、主题仍三态循环、
// 工作区仍可进入/切换/退出），只是把入口从平铺换成一处菜单。
const menuOpen = ref(false)
const menuRef = ref<HTMLElement | null>(null)

// 最近工作区：从会话摘要的归属去重（当前排最前）——不需要后端新接口，
// 侧栏空间分组的数据源即"用户用过哪些工作区"的事实
const recentWorkspaces = computed(() => {
  const set = new Set<string>()
  if (ws.path) set.add(ws.path)
  for (const s of store.summaries) if (s.workspace) set.add(s.workspace)
  return [...set]
})

function onDocMousedown(e: MouseEvent) {
  if (!menuOpen.value) return
  const el = menuRef.value
  if (el && !el.contains(e.target as Node)) menuOpen.value = false
}

// 进入已有工作区：切换工具根并回到草稿。不改当前这场对话的工具——否则侧栏仍挂在旧空间，读写已经打到新目录。
// 0.0.06：生成中不再禁止切换——进行中的会话用自己的工具集（后端按会话持有根），
// 切换只影响"还没落账的新对话"。
async function enterWorkspace(dir: string) {
  menuOpen.value = false
  if (dir === ws.path) return
  const ok = await ws.setPath(dir)
  if (ok) await store.newSession()
}

// 切换工作区：弹系统目录选择框；状态收敛在 workspace store（侧栏"按空间分组"同源）。
// 与侧栏"打开"同语义：切换空间即回到草稿开新对话（归属由首条消息落账本时决定）
async function switchWorkspace() {
  const ok = await ws.pickAndSet()
  if (ok) await store.newSession()
}

// 选择其他目录并新建对话（菜单项）：语义同 switchWorkspace
async function pickWorkspace() {
  menuOpen.value = false
  await switchWorkspace()
}

// 退出工作区：纯对话模式（本地文件工具下线），之后的会话无归属落"会话"区
async function exitWorkspace() {
  menuOpen.value = false
  await ws.clear()
  await store.newSession()
}

// ---- 导出（0.0.06 改版；入口收进图标菜单）----
// 生成中同样可用：导出走账本投影，已落账的部分不丢；运行中只导出"到目前为止"。

// Esc 关闭图标菜单（第 3 批）：经消费栈注册——浮层优先消费，不碰正在跑的回合
useEscClose(menuOpen, () => {
  menuOpen.value = false
})

async function currentMarkdown(): Promise<string | null> {
  if (!store.sessionId) return null
  const md = await store.exportMarkdown(store.sessionId)
  return md || null
}

// 复制 Markdown 到剪贴板；剪贴板失败时提示改用"另存为文件"（不假装成功）
async function exportCopy() {
  menuOpen.value = false
  const md = await currentMarkdown()
  if (!md) return
  await copy(md, { success: '已复制为 Markdown', fail: '剪贴板不可用——可改用「另存为文件」' })
}

// 另存为文件：系统保存对话框走后端（WebView 内下载行为不可控）；剪贴板失败也有出路
async function exportSave() {
  menuOpen.value = false
  const md = await currentMarkdown()
  if (!md) return
  try {
    await bridge().app.SaveTextFile('会话导出.md', md)
    toast('info', '已保存为 Markdown 文件')
  } catch (e) {
    toast('error', errText(e))
  }
}

// 审批闸门开关（ADR-0007 默认关）：开启后 shell 命令执行前需你确认。
// 0.2.27 起同时覆盖 ext_manage（MCP/Skill 增删会让本机执行新命令——它与 shell
// 是同一类"执行面"，不纳入审批等于留了一条绕过确认的路径）。
// 乐观翻转 + 失败回滚：SetApprovalPolicy 失败时开关若停在翻转态，显示与实际策略相反
async function toggleApproval() {
  const next = !approvalOn.value
  approvalOn.value = next
  const ok = await store.setApprovalPolicy(next ? ['shell', 'ext_manage'] : [])
  if (!ok) approvalOn.value = !next
}

// ---- 主题切换（0.0.06）：跟随系统 → 浅色 → 深色 循环；偏好持久化 ----
const themeMode = ref<ThemeMode>(currentTheme())
function toggleTheme() {
  themeMode.value = cycleTheme()
}

onMounted(async () => {
  // 三步各自隔离：启动自检任一步失败都不该打断后面的初始化（尤其审批开关初值
  // 依赖 loadApprovalPolicy）。channels.load / ws.refresh / loadApprovalPolicy
  // 内部均已吞错，这里再兜一层防未处理 rejection。
  try {
    await channels.load()
  } catch {
    /* 渠道读取失败：模型名留空，错误由渠道 store 呈现 */
  }
  try {
    await ws.refresh()
  } catch {
    /* workspace.refresh 已自吞错误，这里只兜意外 */
  }
  approvalOn.value = (await store.loadApprovalPolicy()).length > 0
  document.addEventListener('mousedown', onDocMousedown)
})

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocMousedown)
})
</script>

<template>
  <!-- 无边框窗口标题栏（阶段 1 瘦身）：固定 40px 且 **不换行**（1280 宽也不折两行）；
       不折行靠 flex-nowrap + 中段 min-w-0 truncate + 右侧 shrink-0。
       **这里不能加 overflow-hidden**：图标菜单是 header 内的 absolute 浮层，
       裁切会把整张菜单剪没——点"更多"看起来就像没反应（0.0.17 实机反馈）。
       整条可拖拽（button/input 等交互元素在 style.css 里统一 no-drag） -->
  <header
    class="flex h-10 shrink-0 flex-nowrap items-center gap-3 border-b border-[var(--c-border)] bg-[var(--c-surface)] px-3"
    style="--wails-draggable: drag"
  >
    <!-- 左：产品标记（0.3：终端提示符字形代替裸字母 T——一眼读出"开发者工具"）+ 产品名 -->
    <div class="flex shrink-0 items-center gap-2">
      <span
        class="grid h-6 w-6 shrink-0 select-none place-items-center rounded-[8px] bg-[var(--c-primary)] text-white"
        aria-hidden="true"
      >
        <AppIcon name="terminal" :size="13" />
      </span>
      <h1 class="text-[15px] font-semibold tracking-tight">tiancode</h1>
    </div>

    <!-- 中：本场对话的工作区（路径末段，完整路径进 title）+ Git 分支；两者都没有就不占位 -->
    <div class="flex min-w-0 flex-1 items-center justify-center gap-2 text-xs">
      <template v-if="wsBase || branch">
        <span v-if="wsBase" class="min-w-0 truncate text-[var(--c-text-dim)]" :title="wsFullPath">{{ wsBase }}</span>
        <span v-if="wsBase && branch" class="shrink-0 text-[var(--c-text-faint)]" aria-hidden="true">·</span>
        <span
          v-if="branch"
          class="min-w-0 truncate text-[var(--c-text-dim)]"
          :title="`当前 Git 分支：${branch}`"
          >{{ branch }}</span
        >
      </template>
      <!-- 无根无分支（纯对话会话 / 无工作区草稿）：明示而非冒充、也非空缺 -->
      <span v-else class="min-w-0 truncate text-[var(--c-text-faint)]" :title="wsLabel.mainTitle">
        {{ wsLabel.main }}
      </span>
    </div>

    <!-- 右：状态点 + 油表 + 图标菜单 + 窗口控制（固定不收缩——不换行的保证在这里） -->
    <div class="flex shrink-0 items-center gap-2">
      <!-- 状态点（阶段 1）：空闲只留点，运行/渠道/模型全进 title；忙时补一小段文字。
           样式直接用工具类：style.css 里的 .chip 在 CSS 层之外，工具类覆盖不了它的颜色。 -->
      <button
        class="inline-flex items-center gap-1.5 rounded-[var(--r-pill)] border px-3 py-1.5 transition-colors enabled:hover:bg-[var(--c-surface-soft)] disabled:cursor-default"
        :class="
          store.anyPending
            ? 'border-[var(--c-warn)] text-[var(--c-warn-text)]'
            : store.anyRunning
              ? 'border-[var(--c-primary)] text-[var(--c-primary)]'
              : 'border-[var(--c-border)] text-[var(--c-text-dim)]'
        "
        :disabled="!store.busyTarget"
        :title="statusTitle"
        :aria-label="statusTitle"
        @click="jumpToBusy"
      >
        <span class="h-1.5 w-1.5 rounded-full" :class="statusDotClass"></span>
        <span v-if="busyLabel" class="text-[11px]">{{ busyLabel }}</span>
      </button>
      <!-- 油表（阶段 1：细进度条）：条 = 剩余比例，全文（tok / 上限 / 剩余 / 折叠）在 title；
           没有读数不显示，没配上限只给一句短文案 -->
      <span v-if="usage" class="hidden items-center gap-2 sm:flex" :title="usage.full">
        <span class="h-1.5 w-16 overflow-hidden rounded-full bg-[var(--c-surface-soft)]">
          <span class="block h-full rounded-full" :class="usage.bar" :style="{ width: usage.pct + '%' }"></span>
        </span>
        <span class="text-[11px] text-[var(--c-text-faint)]">{{ usage.short }}</span>
      </span>
      <!-- 图标菜单（阶段 1）：导出 / 命令确认 / 主题 / 工作区收进这一处。
           行为与改前完全一致，只是不再各占一个顶栏按钮（那是折行的主因）。 -->
      <div ref="menuRef" class="relative">
        <button
          class="chip"
          aria-haspopup="menu"
          :aria-expanded="menuOpen"
          title="更多：导出 / 命令确认 / 主题 / 工作区"
          aria-label="更多操作"
          @click="menuOpen = !menuOpen"
        >
          <AppIcon name="menu" :size="14" />
        </button>
        <div
          v-if="menuOpen"
          role="menu"
          class="absolute right-0 top-full z-40 mt-1 max-h-[70vh] w-72 overflow-y-auto rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] p-1.5 shadow-[var(--shadow-float)]"
        >
          <!-- 导出（0.0.06）：复制 / 另存文件二选一；生成中也可用（导出已落账部分） -->
          <button
            role="menuitem"
            class="menu-item"
            :disabled="!store.sessionId"
            title="导出当前会话为 Markdown（生成中可导出已落账部分）"
            @click="exportCopy"
          >
            <AppIcon name="copy" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="flex-1 text-left">复制 Markdown</span>
            <span class="shrink-0 text-[10px] text-[var(--c-text-faint)]">导出</span>
          </button>
          <button role="menuitem" class="menu-item" :disabled="!store.sessionId" @click="exportSave">
            <AppIcon name="download" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="flex-1 text-left">另存为文件…</span>
            <span class="shrink-0 text-[10px] text-[var(--c-text-faint)]">导出</span>
          </button>
          <div class="my-1 h-px bg-[var(--c-border)]"></div>
          <!-- 审批闸门（ADR-0007）：开启后仍同时覆盖 shell 与 ext_manage -->
          <button
            role="menuitem"
            class="menu-item"
            :aria-pressed="approvalOn"
            title="开启后 shell 命令与扩展增删（ext_manage）执行前需你确认"
            @click="toggleApproval"
          >
            <AppIcon name="shield" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="flex-1 text-left">命令确认</span>
            <span
              class="shrink-0 text-[11px]"
              :class="approvalOn ? 'text-[var(--c-primary)]' : 'text-[var(--c-text-faint)]'"
            >
              {{ approvalOn ? '开' : '关' }}
            </span>
          </button>
          <!-- 主题三态（0.0.06）：跟随系统 / 浅色 / 深色循环；不关菜单，方便连点循环回来 -->
          <button role="menuitem" class="menu-item" @click="toggleTheme">
            <AppIcon name="refresh" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="flex-1 text-left">主题</span>
            <span class="shrink-0 text-[11px] text-[var(--c-text-faint)]">{{ THEME_LABEL[themeMode] }}</span>
          </button>
          <div class="my-1 h-px bg-[var(--c-border)]"></div>
          <!-- 工作区（0.0.07 语义不变）：主信息在顶栏中段（路径末段），这里做进入/切换/退出 -->
          <div class="px-2 py-1 text-[11px] text-[var(--c-text-faint)]">
            工作区（点选后打开新对话，当前这场不会改目录）
          </div>
          <p
            v-if="wsLabel.sub"
            class="px-2 py-1 text-[11px] text-[var(--c-text-faint)]"
            :title="wsLabel.subTitle"
          >
            {{ wsLabel.sub }}
          </p>
          <p v-if="!recentWorkspaces.length" class="px-2 py-1.5 text-xs text-[var(--c-text-dim)]">
            还没有工作区——选择目录后，新对话将归属它；不选则按通用会话处理
          </p>
          <button v-for="w in recentWorkspaces" :key="w" role="menuitem" class="menu-item" @click="enterWorkspace(w)">
            <AppIcon name="folder" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="min-w-0 flex-1 truncate text-left">{{ w }}</span>
            <span v-if="w === ws.path" class="shrink-0 text-[10px] text-[var(--c-primary)]">当前</span>
          </button>
          <button role="menuitem" class="menu-item" @click="pickWorkspace">
            <AppIcon name="folder" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="flex-1 text-left">选择其他目录并新建对话…</span>
          </button>
          <button
            v-if="ws.path"
            role="menuitem"
            class="menu-item text-[var(--c-err-text)]"
            @click="exitWorkspace"
          >
            <AppIcon name="x" :size="13" class="shrink-0" />
            <span class="flex-1 text-left">退出工作区（纯对话）</span>
          </button>
        </div>
      </div>

      <!-- 窗口控制（无边框自绘）：最小化 / 最大化还原 / 关闭 -->
      <div class="ml-1 flex shrink-0 items-center gap-0.5">
        <button class="win-btn" aria-label="最小化" title="最小化" @click="winMinimize">
          <AppIcon name="minus" :size="14" />
        </button>
        <button class="win-btn" aria-label="最大化或还原" title="最大化 / 还原" @click="winToggleMaximize">
          <AppIcon name="stop" :size="11" />
        </button>
        <button class="win-btn win-btn-close" aria-label="关闭窗口" title="关闭" @click="winClose">
          <AppIcon name="x" :size="14" />
        </button>
      </div>
    </div>

  </header>
</template>
