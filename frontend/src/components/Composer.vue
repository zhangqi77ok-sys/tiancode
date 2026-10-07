<script setup lang="ts">
import { computed, inject, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useCatalogStore } from '../stores/catalog'
import { useChannelStore } from '../stores/channels'
import { useChatStore, type ForcedToolDTO, type PendingAttachment } from '../stores/chat'
import { appendToDraft } from '../composables/appendSelection'
import { parseAtToken, applyPick, type AtToken } from '../composables/atFile'
import { applySlashPick, parseSlashToken, type SlashToken } from '../composables/slashCmd'
import { useDialogs } from '../composables/useDialogs'
import { useEscClose } from '../composables/useEsc'
import { errText } from '../composables/errText'
import { useToast } from '../composables/useToast'
import { useContextGauge } from '../composables/useContextGauge'
import { bridge } from '../wails'
import AppIcon from './AppIcon.vue'

// 输入框高度（0.0.06）：默认约 3 行（72px）；可拖到大约半屏（50vh）。
// 自动增高与手动拖拽并存：autoGrow 封顶 50vh，用户拖过之后以用户为准。
const MIN_INPUT_HEIGHT_PX = 72
const MAX_INPUT_HEIGHT = '50vh'

const store = useChatStore()
const channels = useChannelStore()
// 模型覆盖（0.0.44 换个模型重答）：空 = 跟随默认渠道；选定后本轮发送（含重跑）
// 用指定模型，只影响单轮不改默认——下一轮想要别的模型再选一次。
const overrideModel = ref('')
void channels.load().catch(() => {}) // 加载失败时下拉只有"跟随默认"，不阻断输入

// 调研进行中按钮保持亮着，点它不能把这一轮改成可写（后端已经按只读发出去了）。
const planTitle = computed(() => {
  if (store.planTurn === store.sessionId) return '方案调研进行中：这一轮只读，结束后再确认是否执行'
  if (store.planMode) return '方案模式已开：发送后只读调研并给出方案，确认后才改文件'
  return '方案模式：只读调研、结束时给方案，确认后再执行'
})
function togglePlan() {
  if (store.planTurn === store.sessionId) return
  store.planMode = !store.planMode
}
const draft = defineModel<string>({ required: true })
const box = ref<HTMLTextAreaElement | null>(null)

// 重跑（第 7 批）：App 把「待重跑锚点 + 这条消息原有的附件」传进来。点气泡的「重跑」
// 只做回填——撤回与账本分叉要等用户改完文字、按下发送并确认之后才发生。
const props = defineProps<{ rerun?: { seq: number; attachments: PendingAttachment[] } | null }>()
const emit = defineEmits<{ (e: 'rerun-done'): void }>()
const dialogs = useDialogs()
const { push: toast } = useToast()

// 注册到根组件：全局快捷键（Ctrl/Cmd+I）聚焦输入框用
const registerInput = inject<(el: HTMLTextAreaElement | null) => void>('registerComposerInput', () => {})
function setBoxRef(el: unknown) {
  box.value = el as HTMLTextAreaElement | null
  registerInput(box.value)
}

// ---- @ 文件引用（0.0.09，0.0.10 会话化）：候选来自这场对话自己的工作区 ----
const atFile = ref<AtToken | null>(null)
const atHits = ref<string[]>([])
const atIndex = ref(0)
const atNotice = ref('') // 纯对话等场景的提示（不报错弹窗）
let atTimer: ReturnType<typeof setTimeout> | null = null
// 搜索代际：每次光标变化作废上一代——防抖回调里的慢响应返回时，若期间用户
// 已改关键词/关掉 @，结果必须丢弃，否则旧关键词的命中会覆盖新菜单（FileTreePanel
// 的 gen 纪律同款）；卸载时也要清掉挂起的防抖定时器
let atSeq = 0

function onCaretChange() {
  const t = box.value
  if (!t) return
  refreshSlash() // / 技能与 MCP 菜单（纯本地解析，无 IPC）
  const token = parseAtToken(draft.value, t.selectionStart ?? 0)
  atFile.value = token
  const seq = ++atSeq // 本代开始：旧代在途响应作废
  if (!token) {
    atHits.value = []
    atNotice.value = ''
    return
  }
  if (atTimer) clearTimeout(atTimer)
  atTimer = setTimeout(async () => {
    atTimer = null
    try {
      const hits = (await bridge().app.SearchWorkspaceFiles(store.sessionId, token.query)) ?? []
      if (seq !== atSeq) return // 关键词已变：过期结果丢弃
      atHits.value = hits
      atIndex.value = 0
      atNotice.value = atHits.value.length ? '' : '没有匹配的文件'
    } catch (e) {
      if (seq !== atSeq) return
      // 纯对话没有工作区：提示而非报错，也不去用顶栏里下一场新对话的根
      atHits.value = []
      atNotice.value = errText(e)
    }
  }, 150)
}

function applyAtPick(path: string) {
  const t = box.value
  if (!t || !atFile.value) return
  const r = applyPick(draft.value, t.selectionStart ?? 0, atFile.value, path)
  draft.value = r.text
  atFile.value = null
  atHits.value = []
  nextTickFocus(r.caret)
}
function nextTickFocus(caret: number) {
  requestAnimationFrame(() => {
    if (box.value) {
      box.value.focus()
      box.value.setSelectionRange(caret, caret)
    }
  })
}
function atKeydown(e: KeyboardEvent): boolean {
  if (!atFile.value || !atHits.value.length) return false
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    atIndex.value = (atIndex.value + (e.key === 'ArrowDown' ? 1 : atHits.value.length - 1)) % atHits.value.length
    return true
  }
  if (e.key === 'Enter' && !e.isComposing) {
    e.preventDefault()
    applyAtPick(atHits.value[atIndex.value])
    return true
  }
  if (e.key === 'Escape') {
    e.preventDefault()
    atFile.value = null
    atHits.value = []
    return true
  }
  return false
}

// ---- / 技能与 MCP（第 7 批）：本轮"开口前先调用" ----
// 与 @ 文件引用同一层交互：输入 / 弹菜单，只列**已启用**的技能与已启用的 MCP 服务器。
// 选中后收成一张 chip（正文里不留命令字样），发送时作为强制工具参数下发——由程序在
// 模型开口前调用现有 skill / mcp 工具，结果按普通工具卡进上下文。
// 一条消息最多指定一个（再选即替换）；ext_manage 刻意不进菜单（增删扩展仍只在侧栏）。
const catalog = useCatalogStore()
interface ForcedTool {
  kind: 'skill' | 'mcp'
  name: string // 技能名 / MCP 服务器名
  tool: string // MCP 工具名（skill 为空）
  args: string // 用户填的 arguments JSON（原样传入；空 = {}）
}
interface SlashItem {
  kind: 'skill' | 'mcp'
  name: string
  hint: string
}
const slash = ref<SlashToken | null>(null)
const slashMenuOpen = ref(false)
const slashStage = ref<'root' | 'tool'>('root')
const slashServer = ref('') // 第二步：已选的 MCP 服务器
const slashIndex = ref(0)
const pickToolName = ref('') // 第二步：手输的工具名（远程服务器不支持自动列工具）
const forcedTool = ref<ForcedTool | null>(null)
// 第二步的工具清单（第 8 批）：选中服务器后由 ProbeServer 现场列出——这不是一次
// 对话发送（不写账本、不产生工具卡），也不占这一轮。
const probeTools = ref<string[]>([])
const probeMsg = ref('') // 探测结果说明（远程服务器给现成的「不支持自动列工具」原文）
const probing = ref(false)

const slashItems = computed<SlashItem[]>(() => {
  const q = (slash.value?.query ?? '').toLowerCase()
  const out: SlashItem[] = []
  for (const s of catalog.skills) {
    if (s.enabled && (!q || s.name.toLowerCase().includes(q))) out.push({ kind: 'skill', name: s.name, hint: s.description || '' })
  }
  for (const m of catalog.mcp) {
    if (m.enabled && (!q || m.name.toLowerCase().includes(q))) out.push({ kind: 'mcp', name: m.name, hint: m.url || m.command || '' })
  }
  return out
})

function closeSlash() {
  slashMenuOpen.value = false
  slashStage.value = 'root'
  slashServer.value = ''
  pickToolName.value = ''
  probeTools.value = []
  probeMsg.value = ''
  probing.value = false
}

// 选中服务器后当场列出它公布的工具（第 8 批）：调用现成的 MCPTool.ProbeServer 绑定。
// 只读探测，失败（含远程服务器的「不支持自动列工具」）原样显示给用户，
// 并保留手打工具名——不能因为列不出来就把这条路堵死。
async function probeServerTools(name: string) {
  probing.value = true
  probeTools.value = []
  probeMsg.value = ''
  try {
    probeTools.value = (await bridge().app.ProbeMcpServer(name)) ?? []
    if (!probeTools.value.length) probeMsg.value = '该服务器没有公布工具，请手打工具名'
  } catch (e) {
    probeMsg.value = errText(e)
  } finally {
    probing.value = false
  }
}

// 光标变化时刷新菜单：/ 与 @ 互斥（同一段文字里只可能有一个）
function refreshSlash() {
  const t = box.value
  if (!t) return
  const caret = t.selectionStart ?? 0
  const st = parseSlashToken(draft.value, caret)
  if (!st || parseAtToken(draft.value, caret)) {
    if (slashMenuOpen.value) closeSlash()
    slash.value = null
    return
  }
  slash.value = st
  slashMenuOpen.value = true
  slashStage.value = 'root'
  if (slashIndex.value >= slashItems.value.length) slashIndex.value = 0
}

function pickSlashItem(item: SlashItem) {
  const st = slash.value
  if (!st) return
  const r = applySlashPick(draft.value, st)
  draft.value = r.text
  slash.value = null
  if (item.kind === 'skill') {
    forcedTool.value = { kind: 'skill', name: item.name, tool: '', args: '' }
    closeSlash()
    nextTickFocus(r.caret)
    return
  }
  // MCP：先选服务器，再选工具——工具清单由 ProbeServer 当场列出（不花一轮 tool=list）
  slashServer.value = item.name
  slashStage.value = 'tool'
  pickToolName.value = ''
  slashMenuOpen.value = true
  nextTickFocus(r.caret)
  void probeServerTools(item.name)
}

function pickMcpTool(tool: string) {
  const server = slashServer.value.trim()
  const t = tool.trim()
  if (!server || !t) return
  forcedTool.value = { kind: 'mcp', name: server, tool: t, args: forcedTool.value?.kind === 'mcp' ? forcedTool.value.args : '' }
  closeSlash()
}

function slashKeydown(e: KeyboardEvent): boolean {
  if (!slashMenuOpen.value) return false
  if (e.key === 'Escape') {
    e.preventDefault()
    closeSlash()
    return true
  }
  if (slashStage.value !== 'root') return false // 第二步由输入框自己的 Enter 处理
  const n = slashItems.value.length
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    if (n) slashIndex.value = (slashIndex.value + (e.key === 'ArrowDown' ? 1 : n - 1)) % n
    return true
  }
  if (e.key === 'Enter' && !e.isComposing) {
    e.preventDefault()
    const it = slashItems.value[slashIndex.value]
    if (it) pickSlashItem(it)
    return true
  }
  return false
}

// 强制工具转 IPC 形态：skill → {name}；mcp → {server, tool, arguments}。
// 参数 JSON 填错时抛错（调用方中止发送并提示）——绝不静默降级成"没指定"。
function buildForcedPayload(): ForcedToolDTO | undefined {
  const f = forcedTool.value
  if (!f) return undefined
  if (f.kind === 'skill') return { name: 'skill', arguments: { name: f.name } }
  const payload: Record<string, unknown> = { server: f.name, tool: f.tool }
  // 第 8 批：新界面不给参数框——参数由模型按工具说明填（后端钉住 server/tool）。
  // 旧界面遗留的手写参数如果还在，仍原样带上（后端走"程序代发"那条老路）。
  const raw = f.args.trim()
  if (raw) {
    try {
      payload.arguments = JSON.parse(raw)
    } catch {
      throw new Error('MCP 参数不是合法 JSON，请改好再发')
    }
  }
  return { name: 'mcp', arguments: payload }
}

// ---- 附件（0.0.10）：粘贴/拖放/上传三路进入同一个待发送区 ----
const atts = ref<PendingAttachment[]>([])

// 方案调研时补的话如果没发出去，终态后放回输入框（可能要等切回这场对话）。
function applyPlanDraftRestore() {
  const back = store.takeDraftRestore(store.sessionId)
  if (!back) return
  draft.value = draft.value.trim() ? `${draft.value.replace(/\s+$/, '')}\n${back.text}` : back.text
  if (back.atts.length) atts.value = [...atts.value, ...back.atts]
}
watch(() => store.draftRestoreNonce, applyPlanDraftRestore)
watch(() => store.sessionId, applyPlanDraftRestore)
const fileInput = ref<HTMLInputElement | null>(null)
const dragOver = ref(false)

function fmtSize(n: number): string {
  return n >= 1024 * 1024 ? `${(n / 1024 / 1024).toFixed(1)}MB` : `${Math.max(1, Math.round(n / 1024))}KB`
}

function blobToAttachment(kind: 'image' | 'file', name: string, blob: Blob): Promise<PendingAttachment> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const dataUrl = String(reader.result)
      resolve({
        kind,
        name,
        mediaType: blob.type || 'application/octet-stream',
        size: blob.size,
        dataB64: dataUrl.slice(dataUrl.indexOf(',') + 1),
        inline: kind === 'image' ? 'none' : (blob.size <= 256 * 1024 ? 'full' : 'path'),
      })
    }
    reader.onerror = () => reject(new Error(`读取 ${name} 失败`))
    reader.readAsDataURL(blob)
  })
}

function pathAttachment(name: string, blob: Blob, path: string): Promise<PendingAttachment> {
  return blobToAttachment('file', name, blob).then((a) => ({ ...a, sourcePath: path, dataB64: undefined }))
}

async function addFiles(items: { kind: 'image' | 'file'; name: string; blob: Blob; path?: string }[]) {
  for (const it of items) {
    try {
      const a = it.path ? await pathAttachment(it.name, it.blob, it.path) : await blobToAttachment(it.kind, it.name, it.blob)
      atts.value.push(a)
    } catch {
      atts.value.push({
        kind: it.kind, name: it.name, mediaType: it.blob.type || 'application/octet-stream',
        size: it.blob.size, sourcePath: it.path, inline: 'path',
      })
    }
  }
}

function onPaste(e: ClipboardEvent) {
  const items = e.clipboardData?.items
  if (!items) return
  const collected: { kind: 'image' | 'file'; name: string; blob: Blob; path?: string }[] = []
  let hasImage = false
  for (let i = 0; i < items.length; i++) {
    const it = items[i]
    if (it.kind === 'file') {
      const f = it.getAsFile()
      if (!f) continue
      if (f.type.startsWith('image/')) {
        hasImage = true
        collected.push({ kind: 'image', name: f.name || `截图.${f.type.split('/')[1] || 'png'}`, blob: f })
      } else {
        collected.push({ kind: 'file', name: f.name || '粘贴文件', blob: f, path: (f as unknown as { path?: string }).path })
      }
    }
  }
  if (hasImage || collected.some((c) => c.kind === 'file')) {
    e.preventDefault() // 收下附件（普通文字粘贴不拦截）
    void addFiles(collected)
  }
}

function onDrop(e: DragEvent) {
  e.preventDefault()
  dragOver.value = false
  const files = e.dataTransfer?.files
  if (!files?.length) return
  const collected: { kind: 'image' | 'file'; name: string; blob: Blob; path?: string }[] = []
  for (let i = 0; i < files.length; i++) {
    const f = files[i]
    const anyF = f as unknown as { path?: string }
    collected.push({
      kind: f.type.startsWith('image/') ? 'image' : 'file',
      name: f.name, blob: f, path: anyF.path,
    })
  }
  void addFiles(collected)
}

function onFilePick(e: Event) {
  const input = e.target as HTMLInputElement
  const files = input.files
  if (!files) return
  for (let i = 0; i < files.length; i++) {
    const f = files[i]
    const anyF = f as unknown as { path?: string }
    void addFiles([{
      kind: f.type.startsWith('image/') ? 'image' : 'file',
      name: f.name, blob: f, path: anyF.path,
    }])
  }
  input.value = ''
}

function removeAt(i: number) {
  atts.value.splice(i, 1)
}
function clearAtts() {
  atts.value = []
}
defineExpose({ onPaste, onDrop })

// ---- 模型选择器（0.2.28 用户反馈：这里的模型应该是已有的模型，可以支持选择）----
// 数据源 = 全部可用渠道的模型列表；切换 = 激活对应渠道并指定模型（后端 SetActiveModel）。
// 显示的模型即实际运行的模型（后端回传 defaultModel，不再各说各话）。
// 0.3 驾驶位：选择器收进输入框左侧——当前模型就是驾驶位的第一读数；渠道/Key/代理
// 仍只在「渠道管理」里维护，这里只负责"切模型"这一件事。
const modelMenuOpen = ref(false)
const modelMenuRef = ref<HTMLElement | null>(null)
const modelOptions = computed(() => channels.modelOptions)

// 上下文余量（0.3）：与顶栏油表同一份数据、同一套文案（useContextGauge）。
// 顶栏油表在 1280 以下被响应式隐藏，输入框上的这份必须始终可见。
const { usage } = useContextGauge()

// Esc 关闭模型菜单（第 3 批）：经消费栈注册（原先只有点外部关闭）
useEscClose(modelMenuOpen, () => {
  modelMenuOpen.value = false
})

function isCurrent(opt: { channelId: string; model: string }): boolean {
  return channels.activeChannel?.id === opt.channelId && channels.activeModel === opt.model
}

async function pickModel(opt: { channelId: string; model: string }) {
  modelMenuOpen.value = false
  if (isCurrent(opt)) return
  await channels.setActiveModel(opt.channelId, opt.model)
}

function onDocMousedown(e: MouseEvent) {
  if (!modelMenuOpen.value) return
  const el = modelMenuRef.value
  if (el && !el.contains(e.target as Node)) modelMenuOpen.value = false
}

// ---- 选中文字放进输入框（第 7 批）----
// 对话区（MessageList 带 data-conversation）里选中一段文字后，输入框旁出现一个按钮：
// 把选区**追加**到草稿末尾（已有内容则空一行），不替换整段草稿、不自动发送。
// 选区为空、或选的是输入框/面板里的文字时不出现。
const selectionText = ref('')
function refreshSelection() {
  const sel = document.getSelection()
  const text = sel && !sel.isCollapsed ? sel.toString() : ''
  if (!text.trim()) {
    selectionText.value = ''
    return
  }
  const node = sel?.anchorNode ?? null
  const el = node instanceof Element ? node : (node?.parentElement ?? null)
  selectionText.value = el?.closest('[data-conversation]') ? text : ''
}
function putSelectionIntoDraft() {
  if (!selectionText.value) return
  draft.value = appendToDraft(draft.value, selectionText.value)
  selectionText.value = ''
  requestAnimationFrame(() => {
    const t = box.value
    if (!t) return
    t.focus()
    const end = draft.value.length
    t.setSelectionRange(end, end)
  })
}

onMounted(() => {
  document.addEventListener('mousedown', onDocMousedown)
  document.addEventListener('selectionchange', refreshSelection)
})
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocMousedown)
  document.removeEventListener('selectionchange', refreshSelection)
  if (atTimer) {
    clearTimeout(atTimer) // 卸载不再触发挂起的 @ 搜索：回调会写已脱离视图的菜单状态
    atTimer = null
  }
})

// 自动增高且不超过半屏；发送后复位到默认高度。输入同时刷新 @ 引用状态。
function autoGrow(e: Event) {
  const t = e.target as HTMLTextAreaElement
  t.style.height = 'auto'
  t.style.height = Math.min(t.scrollHeight, window.innerHeight / 2) + 'px'
  onCaretChange()
}

// Enter 发送 / Shift+Enter 换行（ChatGPT/Cursor/Cline 通用惯例，替代旧版 Ctrl+Enter）。
// isComposing 保护：中文输入法选词时的 Enter 是候选确认，不是发送意图——必须放行。
// @ 浮层打开时：上下/Enter/Escape 归浮层（优先于发送）。
function onComposerKeydown(e: KeyboardEvent) {
  if (slashKeydown(e)) return
  if (atKeydown(e)) return
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    void submit()
  }
}

function resetBox() {
  if (box.value) box.value.style.height = MIN_INPUT_HEIGHT_PX + 'px'
}

async function submit() {
  const text = draft.value.trim()
  const curAtts = atts.value
  if (!text && !curAtts.length) return
  let forced: ForcedToolDTO | undefined
  try {
    forced = buildForcedPayload()
  } catch (e) {
    toast('error', errText(e))
    return // 参数不合法：不发送，也不清空（让用户改）
  }
  if (props.rerun) {
    await submitRerun(text, curAtts, forced)
    return
  }
  if (store.running) {
    // 回合进行中：入队（终态后自动依次发出）；附件与指定工具随文字一起入队并清空——
    // 只排文字会让续发的那条丢掉截图/文件与"先调这个技能/MCP"的指定（0.0.11 / 第 7 批）
    store.enqueue(text, curAtts, forced)
    draft.value = ''
    atts.value = []
    forcedTool.value = null
    resetBox()
    return
  }
  draft.value = ''
  atts.value = []
  resetBox()
  try {
    await store.send(text, curAtts, { throwOnError: true, forced, model: overrideModel.value || undefined })
    forcedTool.value = null
  } catch {
    // 发送失败：文字/附件/指定工具全部保留在输入区，允许重试
    draft.value = text
    atts.value = curAtts
  }
}

// 重跑回填（第 7 批）：附件放回待发送区（用户没删就随重跑一起发出），
// 焦点与光标落到文末让人直接改。
watch(
  () => props.rerun,
  (r) => {
    if (!r) return
    atts.value = [...r.attachments]
    requestAnimationFrame(() => {
      const t = box.value
      if (!t) return
      t.focus()
      const end = draft.value.length
      t.setSelectionRange(end, end)
    })
  },
  { immediate: true },
)

// 重跑提交（第 7 批）：先确认（取消 → 什么都不撤回，草稿与附件原样留着）；
// 确认后走既有 RerunFrom（撤回其后文件改动 + 账本分叉，旧行不改写），
// 再发送**输入框里的文字**（用户可能已改过），而不是无条件重发原文。
async function submitRerun(text: string, curAtts: PendingAttachment[], forced?: ForcedToolDTO) {
  const r = props.rerun
  if (!r) return
  if (store.running) {
    toast('error', '正在运行中，不能重跑')
    return
  }
  const ok = await dialogs.confirm({
    title: '从这条消息重跑',
    message: '将撤回这条消息之后的所有文件改动（撤不回的会明确列出），旧对话记录作废并重新执行。继续？',
    confirmText: '重跑',
    danger: true,
  })
  if (!ok) return // 取消：不撤回任何东西
  draft.value = ''
  atts.value = []
  resetBox()
  try {
    const res = await store.rerunFrom(r.seq)
    if (res.reverted.length || res.skipped.length) {
      const parts = [`已撤回 ${res.reverted.length} 个文件`]
      if (res.skipped.length) parts.push(`撤不回：${res.skipped.join('；')}`)
      toast(res.skipped.length ? 'error' : 'info', parts.join('；'))
    }
    emit('rerun-done')
    await store.send(text, curAtts, { throwOnError: true, forced, model: overrideModel.value || undefined })
    forcedTool.value = null
  } catch (e) {
    toast('error', errText(e))
    // 失败：文字与附件放回输入区允许重试（与普通发送失败同款）
    draft.value = text
    atts.value = curAtts
  }
}

// 队列编辑：取回文字与附件并出队（焦点回到输入框继续改）。
// 取回的附件排在待发送区最前，原有附件不动（0.0.11——附件随消息走，不能丢）。
function editQueued(id: number) {
  const q = store.editQueued(id)
  if (!q) return
  draft.value = q.text
  atts.value = [...q.atts, ...atts.value]
  box.value?.focus()
}

// ---- 工作区快捷命令（0.0.26）----
// build/test/run 三槽：在工作区设置里配好，这里一键跑。执行走**同一条**链
// （RunQuickCommand → RunUserCommand → 同一 shell 工具/超时/审批闸门）——
// 快捷命令只是"可点的命令"，不新增执行能力。未配置的槽不出现。
// 手动命令行输入已移除（0.0.33 用户裁决）：模型跑命令本就走同一条审批链，
// 重复入口只添杂乱；"自己跑一条"的诉求由对话里直接说（或快捷命令槽）承接。
const quick = ref<{ build: string; test: string; run: string }>({ build: '', test: '', run: '' })
const quickBusy = ref('')
const quickSlot = computed(() => [
  { slot: 'build', cmd: quick.value.build },
  { slot: 'test', cmd: quick.value.test },
  { slot: 'run', cmd: quick.value.run },
] as const)

async function loadQuick() {
  try {
    quick.value = (await bridge().app.QuickCommands(store.sessionId)) ?? { build: '', test: '', run: '' }
  } catch {
    quick.value = { build: '', test: '', run: '' } // 读不到就不显示（不打扰）
  }
}
onMounted(() => void loadQuick())
// 切换会话时换工作区 → 快捷命令随之换（显示的必须是"这场对话"的命令）
watch(() => store.sessionId, () => void loadQuick())
// 设置面板保存后立即重拉（0.0.29）：否则配完 build/test/run 回到输入框仍无按钮，
// 必须切一次会话才现形——用户视角就是"配了没效果"。
watch(() => store.wsSettingsRev, () => void loadQuick())

async function runQuick(slot: string, cmd: string) {
  if (quickBusy.value || !cmd.trim()) return
  quickBusy.value = slot
  try {
    const res = await bridge().app.RunQuickCommand(store.sessionId, slot, cmd)
    if (res?.isError) {
      toast('error', res.output || `${slot} 失败`)
    } else {
      toast('info', `${slot} 完成`)
    }
  } catch (e) {
    toast('error', errText(e))
  } finally {
    quickBusy.value = ''
  }
}
</script>

<template>
  <div data-composer class="border-t border-[var(--c-border)] p-4">
    <!-- 输入队列：进行中提交的待发消息；「提前」= 顶到队首，本轮结束最先发出。
         容器限高（4 行 + 滚动）：队列长了也不得把输入框顶出可视区域（0.0.06） -->
    <div
      v-if="store.queue.length"
      class="mb-2 max-h-36 space-y-1 overflow-y-auto"
      aria-label="输入队列"
    >
      <div class="flex items-center gap-1.5 px-1 text-[11px] text-[var(--c-text-faint)]">
        <AppIcon name="message" :size="11" /> 队列 ({{ store.queue.length }})
      </div>
      <div
        v-for="q in store.queue"
        :key="q.id"
        class="flex items-center gap-1.5 rounded-lg border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-2.5 py-1.5 text-xs"
      >
        <span class="min-w-0 flex-1 truncate text-[var(--c-text-dim)]" :title="q.text">{{ q.text }}</span>
        <!-- 提前（第 7 批）：只是顶到队首，本轮结束才发——图标与文案都不能像"立刻发送"，
             否则用户以为它会另开一轮（send 图标 + 「立即发送」正是这个误解的来源） -->
        <button
          class="shrink-0 rounded p-1 text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-primary)]"
          title="提前：本轮结束后最先发出"
          aria-label="提前：本轮结束后最先发出"
          @click="store.promoteQueued(q.id)"
        >
          <AppIcon name="chevron-down" :size="12" class="rotate-180" />
        </button>
        <button
          class="shrink-0 rounded p-1 text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-primary)]"
          title="取回编辑"
          aria-label="编辑该消息"
          @click="editQueued(q.id)"
        >
          <AppIcon name="pencil" :size="12" />
        </button>
        <button
          class="shrink-0 rounded p-1 text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-err-text)]"
          title="删除"
          aria-label="删除该消息"
          @click="store.removeQueued(q.id)"
        >
          <AppIcon name="trash" :size="12" />
        </button>
      </div>
    </div>

    <!-- 本轮指定的技能 / MCP（第 7 批）：发送后模型开口前先调用它；MCP 可填参数 JSON -->
    <div v-if="forcedTool" class="mb-2 flex flex-wrap items-center gap-2" aria-label="本轮指定的工具">
      <span
        class="stat gap-1.5 text-[11px]"
        :title="
          forcedTool.kind === 'skill'
            ? '本轮先读取该技能正文'
            : '本轮第一笔调用固定为该 MCP 工具，参数由模型按工具说明填'
        "
      >
        <AppIcon :name="forcedTool.kind === 'skill' ? 'book' : 'plug'" :size="11" />
        {{ forcedTool.kind === 'skill' ? `技能 ${forcedTool.name}` : `MCP ${forcedTool.name} · ${forcedTool.tool}` }}
      </span>
      <button class="chip text-[11px]" title="移除本轮指定的工具" @click="forcedTool = null">移除</button>
    </div>

    <!-- 待发送附件区（0.0.10）：图片缩略图/文件名+大小+内联标记；可单个移除或全部移除 -->
    <div v-if="atts.length" class="mb-2 flex flex-wrap items-center gap-2" aria-label="待发送附件">
      <template v-for="(a, i) in atts" :key="`${a.name}-${i}`">
        <div
          v-if="a.kind === 'image' && a.dataB64"
          class="group relative"
          :title="`${a.name} · ${fmtSize(a.size)}`"
        >
          <img
            :src="`data:${a.mediaType};base64,${a.dataB64}`"
            class="h-14 w-14 rounded-lg border border-[var(--c-border)] object-cover"
            :alt="a.name"
          />
          <span class="absolute bottom-0 left-0 right-0 rounded-b-lg bg-[var(--c-overlay-deep)] px-1 text-center text-[9px] text-white">
            {{ fmtSize(a.size) }}
          </span>
          <button
            class="absolute -right-1.5 -top-1.5 grid h-4.5 w-4.5 place-items-center rounded-full bg-[var(--c-err)] text-white"
            aria-label="移除图片"
            @click="removeAt(i)"
          >
            <AppIcon name="x" :size="9" />
          </button>
        </div>
        <div
          v-else
          class="flex items-center gap-1.5 rounded-lg border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-2.5 py-1.5 text-xs"
        >
          <AppIcon name="file" :size="12" class="shrink-0 text-[var(--c-text-dim)]" />
          <span class="max-w-[10rem] truncate" :title="a.sourcePath || a.name">{{ a.name }}</span>
          <span class="shrink-0 text-[var(--c-text-faint)]">{{ fmtSize(a.size) }}</span>
          <span class="shrink-0 rounded bg-[var(--c-primary-soft)] px-1 text-[10px] text-[var(--c-primary)]">
            {{ a.inline === 'full' ? '将内联' : '只附路径' }}
          </span>
          <button
            class="shrink-0 rounded p-0.5 text-[var(--c-text-faint)] hover:text-[var(--c-err-text)]"
            aria-label="移除附件"
            @click="removeAt(i)"
          >
            <AppIcon name="x" :size="10" />
          </button>
        </div>
      </template>
      <button v-if="atts.length > 1" class="chip text-[11px]" @click="clearAtts">全部移除</button>
    </div>

    <!-- 选中文字放进输入框（第 7 批）：只追加到草稿末尾，不替换、不自动发送。
         mousedown.prevent 与 @ / 菜单选项同款：不 prevent 的话，按下鼠标会先清掉
         选区 → selectionchange 把 selectionText 置空 → 按钮在 click 之前被拆掉，
         文字永远进不了草稿（点了没反应）。 -->
    <button
      v-if="selectionText"
      class="mb-2 chip self-start text-[11px]"
      title="把选中的文字追加到输入框末尾（不发送）"
      @mousedown.prevent
      @click="putSelectionIntoDraft"
    >
      <AppIcon name="plus" :size="11" /> 放进输入框
    </button>

    <div class="relative flex items-end gap-2" :class="dragOver ? 'rounded-[var(--r-input)] ring-2 ring-[var(--c-primary)]' : ''"
      @dragover.prevent="dragOver = true"
      @dragleave.prevent="dragOver = false"
      @drop="onDrop"
    >
      <!-- 模型选择器（0.3 驾驶位，从输入框上方收进输入行左侧）：当前模型是驾驶位
           第一读数，点击只切换模型——渠道/Key/代理仍在「渠道管理」里，不搬进来 -->
      <div ref="modelMenuRef" class="relative shrink-0">
        <button
          class="inline-flex max-w-[11rem] items-center gap-1.5 rounded-[var(--r-pill)] border border-[var(--c-border)] bg-[var(--c-surface)] px-2.5 py-2 text-xs text-[var(--c-text)] transition-colors hover:border-[var(--c-primary)] hover:text-[var(--c-primary)]"
          aria-haspopup="menu"
          :aria-expanded="modelMenuOpen"
          :title="modelOptions.length ? '当前模型，点击切换（来自已配置的渠道）' : '尚未配置模型：请在侧栏底部打开「渠道管理」'"
          @click="modelMenuOpen = !modelMenuOpen"
        >
          <AppIcon name="message" :size="12" class="shrink-0 text-[var(--c-text-dim)]" />
          <span class="min-w-0 truncate font-medium">{{ channels.activeModel || '未选择模型' }}</span>
          <AppIcon name="chevron-down" :size="10" class="shrink-0 text-[var(--c-text-dim)]" />
        </button>
        <div
          v-if="modelMenuOpen"
          role="menu"
          class="absolute bottom-full left-0 z-40 mb-1 max-h-72 w-80 overflow-y-auto rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] p-1.5 shadow-lg"
        >
          <div class="px-2 py-1 text-[11px] text-[var(--c-text-faint)]">选择模型（来自已配置的渠道）</div>
          <p v-if="!modelOptions.length" class="px-2 py-1.5 text-xs text-[var(--c-text-dim)]">
            还没有可用模型——请在侧栏底部打开「渠道管理」添加渠道与模型
          </p>
          <button
            v-for="opt in modelOptions"
            :key="`${opt.channelId}::${opt.model}`"
            role="menuitem"
            class="menu-item"
            @click="pickModel(opt)"
          >
            <AppIcon name="message" :size="12" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="min-w-0 flex-1 truncate text-left">{{ opt.model }}</span>
            <span class="max-w-[8rem] shrink-0 truncate text-[10px] text-[var(--c-text-faint)]">{{ opt.channelName }}</span>
            <span v-if="isCurrent(opt)" class="shrink-0 text-[10px] text-[var(--c-primary)]">当前</span>
          </button>
        </div>
      </div>
      <!-- @ 文件引用浮层（0.0.09） -->
      <div
        v-if="atFile && atHits.length"
        class="absolute bottom-full left-3 z-40 mb-1 max-h-64 w-80 overflow-y-auto rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] p-1.5 shadow-lg"
        role="listbox"
        aria-label="引用工作区文件"
      >
        <div class="px-2 py-1 text-[11px] text-[var(--c-text-faint)]">引用文件（Enter 选中，Esc 关闭）</div>
        <button
          v-for="(h, i) in atHits"
          :key="h"
          role="option"
          :aria-selected="i === atIndex"
          class="menu-item"
          :class="i === atIndex ? 'bg-[var(--c-primary-soft)]' : ''"
          @mousedown.prevent
          @click="applyAtPick(h)"
        >
          <AppIcon name="file" :size="12" class="shrink-0 text-[var(--c-text-faint)]" />
          <span class="min-w-0 flex-1 truncate text-left">{{ h }}</span>
        </button>
      </div>
      <!-- @ 提示条（0.0.11）：无命中/无工作区时必须可见，不再静默失败 -->
      <div
        v-else-if="atFile && atNotice"
        class="absolute bottom-full left-3 z-40 mb-1 rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] px-3 py-2 text-xs text-[var(--c-text-dim)] shadow-lg"
        role="status"
      >
        {{ atNotice }}
      </div>
      <!-- / 技能与 MCP 菜单（第 7 批）：只列已启用项；MCP 先选服务器再选工具 -->
      <div
        v-if="slashMenuOpen"
        class="absolute bottom-full left-3 z-40 mb-1 max-h-64 w-80 overflow-y-auto rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] p-1.5 shadow-lg"
        role="listbox"
        aria-label="指定技能或 MCP"
      >
        <template v-if="slashStage === 'root'">
          <div class="px-2 py-1 text-[11px] text-[var(--c-text-faint)]">
            技能与 MCP（↑↓ 选择，Enter 确定，Esc 关闭）——本轮会先调用它
          </div>
          <p v-if="!slashItems.length" class="px-2 py-1.5 text-xs text-[var(--c-text-dim)]">
            没有已启用的技能或 MCP（增删在侧栏设置里）
          </p>
          <button
            v-for="(it, i) in slashItems"
            :key="it.kind + it.name"
            role="option"
            :aria-selected="i === slashIndex"
            class="menu-item"
            :class="i === slashIndex ? 'bg-[var(--c-primary-soft)]' : ''"
            @mousedown.prevent
            @click="pickSlashItem(it)"
          >
            <AppIcon :name="it.kind === 'skill' ? 'book' : 'plug'" :size="12" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="min-w-0 flex-1 truncate text-left">{{ it.name }}</span>
            <span class="shrink-0 text-[10px] text-[var(--c-text-faint)]">{{ it.kind === 'skill' ? '技能' : 'MCP' }}</span>
          </button>
        </template>
        <template v-else>
          <div class="px-2 py-1 text-[11px] text-[var(--c-text-faint)]">MCP：{{ slashServer }} · 选工具</div>
          <p v-if="probing" class="px-2 py-1.5 text-xs text-[var(--c-text-dim)]">正在列出工具…</p>
          <p v-else-if="probeMsg" class="px-2 py-1.5 text-xs text-[var(--c-text-faint)]">{{ probeMsg }}</p>
          <!-- 探测到的工具名：点名字即绑定 server + tool（参数由模型按工具说明填） -->
          <button
            v-for="t in probeTools"
            :key="t"
            role="option"
            class="menu-item"
            @mousedown.prevent
            @click="pickMcpTool(t)"
          >
            <AppIcon name="plug" :size="12" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="min-w-0 flex-1 truncate text-left">{{ t }}</span>
          </button>
          <!-- 手打工具名：远程服务器不支持自动列工具（上面那句原文就来自它），仍要能绑定 -->
          <div class="flex items-center gap-1.5 px-2 py-1">
            <input
              v-model="pickToolName"
              class="min-w-0 flex-1 rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-2 py-1 text-xs"
              placeholder="工具名（远程服务器需手打）"
              aria-label="MCP 工具名"
              @keydown.enter.prevent="pickMcpTool(pickToolName)"
            />
            <button class="chip text-[11px]" @click="pickMcpTool(pickToolName)">确定</button>
          </div>
        </template>
      </div>
      <textarea
        :ref="setBoxRef"
        v-model="draft"
        rows="3"
        aria-label="消息输入框"
        class="min-w-0 flex-1 resize-y rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-4 py-2.5 text-sm leading-6 transition-colors focus:border-[var(--c-primary)]"
        :style="{ minHeight: MIN_INPUT_HEIGHT_PX + 'px', maxHeight: MAX_INPUT_HEIGHT }"
        placeholder="输入消息…（Enter 发送；@ 引用文件；/ 指定技能或 MCP；可粘贴/拖入图片和文件）"
        @keydown="onComposerKeydown"
        @input="autoGrow"
        @click="onCaretChange"
        @keyup="onCaretChange"
        @paste="onPaste"
      ></textarea>
      <!-- 上下文余量（0.3 驾驶位）：与顶栏油表同源；顶栏那份在 1280 以下被
           sm:flex 隐藏，这一份始终可见。折叠明细在 hover title 里逐项写清。 -->
      <span v-if="usage" class="mb-2 flex shrink-0 flex-col items-end gap-0.5" :title="usage.full">
        <span class="flex items-center gap-1.5">
          <span class="h-1.5 w-14 overflow-hidden rounded-full bg-[var(--c-surface-soft)]">
            <span class="block h-full rounded-full" :class="usage.bar" :style="{ width: usage.pct + '%' }"></span>
          </span>
          <span class="text-[11px] tabular-nums text-[var(--c-text-faint)]">{{ usage.short }}</span>
        </span>
        <span v-if="usage.foldNote" class="text-[10px] text-[var(--c-warn-text)]">折叠：{{ usage.foldNote }}</span>
      </span>
      <!-- 模型选择器（0.0.44）：空 = 跟随默认渠道；选定后发送（含重跑重答）用
           指定模型，只影响单轮不改默认。模型列表来自全部可用渠道的并集。 -->
      <select
        v-model="overrideModel"
        class="chip h-10 max-w-36 shrink-0 text-xs"
        :title="overrideModel ? `本轮将用 ${overrideModel} 发送（不改默认渠道）` : '跟随默认渠道发送'"
        aria-label="选择本轮使用的模型"
      >
        <option value="">跟随默认渠道</option>
        <option v-for="o in channels.modelOptions" :key="o.channelId + '/' + o.model" :value="o.model">
          {{ o.model }}
        </option>
      </select>
      <!-- 方案模式：开着发送 = 只读调研。发出后开关关掉（免得别的会话也只读），
           但这一轮按钮保持亮着，直到终态。确认后才进入真正的执行回合。 -->
      <button
        type="button"
        class="chip h-10 shrink-0"
        :aria-pressed="store.planMode || store.planTurn === store.sessionId"
        :title="planTitle"
        @click="togglePlan"
      >
        <AppIcon name="message" :size="14" /> {{ store.planTurn === store.sessionId ? '方案中' : '方案' }}
      </button>
      <button class="chip h-10 shrink-0" title="上传图片或文件（可多选）" aria-label="上传附件" @click="fileInput?.click()">
        <AppIcon name="plus" :size="14" />
      </button>
      <input ref="fileInput" type="file" multiple class="hidden" @change="onFilePick" />
      <button
        v-if="store.running"
        type="button"
        class="btn-primary h-10 shrink-0 gap-2 px-4 text-sm"
        :disabled="store.stopping"
        @click="store.stop()"
      >
        <AppIcon name="stop" :size="14" /> {{ store.stopping ? '正在中断' : '中断' }}
      </button>
      <button
        v-else
        class="btn-icon shrink-0 disabled:cursor-not-allowed disabled:opacity-50"
        title="发送（Enter）"
        aria-label="发送"
        :disabled="!draft.trim() && !atts.length"
        @click="submit"
      >
        <AppIcon name="send" :size="16" />
      </button>
    </div>

    <!-- 工作区快捷命令（0.0.26）：已配置的槽才出现，整行未配置时不渲染。
         手动命令行输入已移除（0.0.33 用户裁决）——模型跑命令走同一条审批链，
         重复入口只添杂乱；快捷命令保留一键价值。 -->
    <div v-if="quickSlot.some((q) => q.cmd.trim())" class="mt-2 flex items-center gap-2">
      <button
        v-for="q in quickSlot"
        :key="q.slot"
        class="chip shrink-0 text-[11px]"
        :class="quickBusy === q.slot ? 'opacity-60' : ''"
        :disabled="!!quickBusy || !q.cmd.trim()"
        :title="q.cmd.trim() ? `执行：${q.cmd}` : '在工作区设置里配置该命令后才出现'"
        @click="runQuick(q.slot, q.cmd)"
      >
        {{ quickBusy === q.slot ? `${q.slot}…` : q.slot }}
      </button>
    </div>
  </div>
</template>
