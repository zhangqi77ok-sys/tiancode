<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useChannelStore } from '../stores/channels'
import { useDialogs } from '../composables/useDialogs'
import { errText } from '../composables/errText'
import { useClipboard } from '../composables/useClipboard'
import { useToast } from '../composables/useToast'
import { bridge, openExternal } from '../wails'
import type { AuthDTO, ChannelDTO, ChannelHealthDTO, CredentialDTO } from '../wails'
import AppIcon from './AppIcon.vue'
import BaseModal from './BaseModal.vue'

// 渠道设置面板：列表 / 表单 / 凭证管理三视图（单弹窗内切换，不嵌套弹窗）。
// 交互纪律：保存/删除/切换后以后端返回的列表为准（不做乐观更新）；
// 凭证输入框永远留空并提示"留空保持原凭证"——凭证不回显是安全约束。
// 0.2.19（参照 new-api 渠道管理）：
//   - 列表行内"测试"（真实链路，延迟/错误就地显示）与"复制"；
//   - 凭证逐条可见可管（禁用/启用/全量）——自动禁用的恢复途径；
//   - 表单四分区（基本/凭证/模型与路由/高级），autoBan 与映射/覆写随表单落盘。
const emit = defineEmits<{ (e: 'close'): void }>()
const store = useChannelStore()
const dialogs = useDialogs()
const { push: toast } = useToast()
const { copy } = useClipboard()

const view = ref<'list' | 'form' | 'keys' | 'codex'>('list')
const editingID = ref('')

// 已绑定 ChatGPT 账号摘要（codex 渠道编辑时回显；空 = 未绑定/非 codex）
const boundAccount = ref('')

const emptyForm = () => ({
  id: '',
  name: '',
  protocol: 'openai',
  baseUrl: '',
  apiKey: '',
  modelsText: '',
  priority: 100,
  weight: 0,
  status: 'enabled',
  // 0.2.19：新渠道默认开启故障自动禁用（对齐 new-api AutoBan 默认开）
  autoBan: true,
  mappingText: '',
  headerText: '',
  paramText: '',
  // 0.2.20：鉴权方式（default = 协议默认；header/query 需要名称 + 值模板）
  authType: 'default',
  authName: '',
  authValue: '',
  // 第 2 批：上下文上限（token；0 = 不限）——本地派生折叠用，不发给上游
  contextLimit: 0,
  // 0.0.24：每百万 token 单价（成本估算展示用；0 = 未配置）
  priceIn: 0,
  priceOut: 0,
})
const form = ref(emptyForm())

// 协议默认鉴权的展示文案（表单下拉第一项；实际形态由后端适配器决定）
const protocolDefaultAuth = computed(() =>
  form.value.protocol === 'anthropic'
    ? 'x-api-key'
    : form.value.protocol === 'codex'
      ? 'ChatGPT 账号 OAuth（自动）'
      : 'Authorization: Bearer',
)

// 协议默认地址提示（按协议给用户可照抄的地址/说明）
const protocolHint = computed(() => {
  switch (form.value.protocol) {
    case 'anthropic':
      return 'Anthropic Messages 协议：地址形如 https://api.anthropic.com/v1'
    case 'codex':
      return 'Codex 协议（ChatGPT 订阅）：地址留空走官方入口 chatgpt.com/backend-api/codex'
    default:
      return 'OpenAI 兼容协议：地址形如 https://api.deepseek.com/v1'
  }
})

// parseKV 解析"每行 键=值"文本（空行与 # 注释忽略）——模型映射与请求头覆写共用
function parseKV(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const s = line.trim()
    if (!s || s.startsWith('#')) continue
    const i = s.indexOf('=')
    if (i <= 0) continue
    out[s.slice(0, i).trim()] = s.slice(i + 1).trim()
  }
  return out
}

function serializeKV(m?: Record<string, string>): string {
  if (!m) return ''
  return Object.entries(m)
    .map(([k, v]) => `${k}=${v}`)
    .join('\n')
}

function reset() {
  store.clearError()
  store.models = []
  editingID.value = ''
  form.value = emptyForm()
}

function backToList() {
  store.credChannel = ''
  store.credentials = []
  view.value = 'list'
  reset()
}

function startCreate() {
  reset()
  view.value = 'form'
}

// fillFrom 把渠道视图灌入表单（编辑与复制共用；密钥永远留空）
function fillFrom(ch: ChannelDTO) {
  const models = ch.models?.length ? ch.models : [ch.model].filter(Boolean)
  form.value = {
    id: ch.id,
    name: ch.name,
    protocol: ch.protocol,
    baseUrl: ch.baseUrl,
    apiKey: '',
    modelsText: models.join('\n'),
    priority: ch.priority || 100,
    weight: ch.weight || 0,
    status:
      ch.status === 'manually_disabled'
        ? 'manually_disabled'
        : ch.status === 'auto_disabled'
          ? 'auto_disabled'
          : 'enabled',
    autoBan: ch.autoBan ?? false,
    mappingText: serializeKV(ch.modelMapping),
    headerText: serializeKV(ch.headerOverride),
    paramText: ch.paramOverride && Object.keys(ch.paramOverride).length ? JSON.stringify(ch.paramOverride, null, 2) : '',
    authType: ch.auth?.type && ch.auth.type !== 'default' ? ch.auth.type : 'default',
    authName: ch.auth?.name ?? '',
    authValue: ch.auth?.value ?? '',
    contextLimit: ch.contextLimit ?? 0,
    priceIn: ch.priceIn ?? 0,
    priceOut: ch.priceOut ?? 0,
  }
}

// authLabel 列表徽章文案：非默认鉴权才显示（默认不占视觉）
function authLabel(a?: AuthDTO): string {
  if (!a || !a.type || a.type === 'default') return ''
  if (a.type === 'bearer') return 'Bearer'
  if (a.type === 'none') return '无鉴权'
  if (a.type === 'query') return `参数 ${a.name}`
  return `头 ${a.name}`
}

async function startEdit(ch: ChannelDTO) {
  reset()
  editingID.value = ch.id
  fillFrom(ch)
  view.value = 'form'
  // codex 渠道回显已绑定账号（凭证本身绝不回显，只有脱敏摘要）
  if (ch.protocol === 'codex') {
    try {
      const info = await bridge().app.CodexCredentialOf(ch.id)
      boundAccount.value = info?.bound ? (info.display ?? '') : ''
    } catch {
      boundAccount.value = ''
    }
  }
}

// 复制渠道（参照 new-api Copy Channel）：除密钥外全部预填，名称加"副本"后缀
function duplicate(ch: ChannelDTO) {
  reset()
  editingID.value = ''
  fillFrom(ch)
  form.value.id = ''
  form.value.apiKey = ''
  form.value.name = `${ch.name} 副本`
  form.value.status = 'enabled'
  view.value = 'form'
}

function applyPreset(key: string) {
  const p = store.presets.find((x) => x.key === key)
  if (!p) return
  form.value.protocol = p.protocol
  if (p.baseUrl) form.value.baseUrl = p.baseUrl
  if (p.suggestedModel) form.value.modelsText = p.suggestedModel
  if (!form.value.name) form.value.name = p.name
}

// 同步模型：把上游发现的模型填入多行文本框（用户可删减），不做静默替换
async function discover() {
  await store.discover({
    id: form.value.id,
    name: form.value.name,
    protocol: form.value.protocol,
    baseUrl: form.value.baseUrl,
    model: firstModel(),
    apiKey: form.value.apiKey,
  })
  if (store.models.length) form.value.modelsText = store.models.join('\n')
}

function parsedModels(): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const line of form.value.modelsText.split(/[\n,]/)) {
    const m = line.trim()
    if (!m || seen.has(m)) continue
    seen.add(m)
    out.push(m)
  }
  return out
}

function firstModel(): string {
  return parsedModels()[0] ?? ''
}

async function save() {
  const models = parsedModels()
  const mapping = parseKV(form.value.mappingText)
  const headers = parseKV(form.value.headerText)
  let params: Record<string, unknown> = {}
  const paramText = form.value.paramText.trim()
  if (paramText) {
    try {
      params = JSON.parse(paramText) as Record<string, unknown>
    } catch {
      store.error = '参数覆写不是合法 JSON：请检查括号与引号（留空表示不覆写）'
      return
    }
  }
  // 鉴权：default 不入库（保留协议默认语义）；header/query 必须有名称
  let auth: AuthDTO | undefined
  if (form.value.authType !== 'default') {
    if ((form.value.authType === 'header' || form.value.authType === 'query') && !form.value.authName.trim()) {
      store.error = form.value.authType === 'header' ? '自定义鉴权需要填写请求头名称' : '自定义鉴权需要填写 URL 参数名称'
      return
    }
    auth = {
      type: form.value.authType,
      name: form.value.authName.trim() || undefined,
      value: form.value.authValue.trim() || undefined,
    }
  }
  await store.save({
    id: form.value.id,
    name: form.value.name,
    protocol: form.value.protocol,
    baseUrl: form.value.baseUrl,
    apiKey: form.value.apiKey,
    model: models[0] ?? '',
    models,
    priority: form.value.priority,
    weight: form.value.weight,
    status: form.value.status,
    autoBan: form.value.autoBan,
    modelMapping: Object.keys(mapping).length ? mapping : undefined,
    headerOverride: Object.keys(headers).length ? headers : undefined,
    paramOverride: Object.keys(params).length ? params : undefined,
    auth,
    contextLimit: form.value.contextLimit || 0,
    priceIn: form.value.priceIn || 0,
    priceOut: form.value.priceOut || 0,
  })
  if (!store.error) {
    view.value = 'list'
    reset()
  }
}

// 删除渠道：不可逆操作，先确认（对话框替代 window.confirm）
async function remove(ch: ChannelDTO) {
  const ok = await dialogs.confirm({
    title: '删除渠道',
    message: `删除渠道「${ch.name}」？`,
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  await store.remove(ch.id)
  if (editingID.value === ch.id) {
    view.value = 'list'
    reset()
  }
}

function statusLabel(ch: ChannelDTO): string {
  if (ch.status === 'auto_disabled') return '已自动禁用'
  if (ch.status === 'manually_disabled') return '已停用'
  return '启用'
}

function credBadge(ch: ChannelDTO): string {
  const total = ch.credentialCount ?? (ch.hasKey ? 1 : 0)
  const off = ch.credentialDisabled ?? 0
  if (!total) return '未配置凭证'
  return off ? `凭证 ${total} 条 · ${off} 禁用` : `凭证 ${total} 条`
}

// ---- 渠道健康读数（0.0.23）----
// 面板打开时读一次（不进渠道行的每次渲染里拉 IPC）；读失败不打扰面板——健康列
// 显示"暂无数据"，其余功能照常（它只是观测，不该阻塞渠道管理）。
const health = ref<Record<string, ChannelHealthDTO>>({})
onMounted(async () => {
  try {
    health.value = (await bridge().app.ChannelHealth(7)) ?? {}
  } catch {
    health.value = {}
  }
})

function healthOf(id: string): ChannelHealthDTO | undefined {
  return health.value[id]
}
function healthLabel(id: string): string {
  const h = healthOf(id)
  if (!h || h.successRate < 0) return '暂无数据'
  return `近 7 天 ${Math.round(h.successRate * 100)}%`
}
function healthTone(id: string): string {
  const h = healthOf(id)
  if (!h || h.successRate < 0) return ''
  if (h.successRate >= 0.95) return ''
  if (h.successRate >= 0.8) return 'border-[var(--c-warn)] text-[var(--c-warn-text)]'
  return 'border-[var(--c-err)] text-[var(--c-err-text)]'
}
function healthTitle(id: string): string {
  const h = healthOf(id)
  if (!h) return '近 7 天还没有请求记录'
  const total = h.ok + h.fail
  if (total === 0) return '近 7 天还没有请求记录'
  const parts = [
    `近 7 天 ${total} 次请求：成功 ${h.ok} · 失败 ${h.fail}`,
    `渠道级故障 ${h.faults} · 凭证级 ${h.credFaults}`,
    h.avgLatencyMs > 0 ? `平均建流耗时 ${h.avgLatencyMs}ms` : '',
    h.lastError ? `最近错误：${h.lastError}` : '',
  ]
  return parts.filter(Boolean).join('\n')
}

// ---- 凭证管理 ----
const credChannelName = computed(
  () => store.list.find((c) => c.id === store.credChannel)?.name ?? '渠道',
)
const credDisabledCount = computed(() => store.credentials.filter((c) => !c.enabled).length)

// 记录进入来源：从表单进入时返回要回表单（保留未保存的编辑），从列表进入则回列表
const credReturn = ref<'list' | 'form'>('list')

async function openCredentials(id: string, from: 'list' | 'form' = 'list') {
  credReturn.value = from
  view.value = 'keys'
  await store.loadCredentials(id)
}

function credBack() {
  view.value = credReturn.value
  if (credReturn.value === 'list') reset() // 回列表：清理表单；回表单：保留编辑态
}

async function toggleCred(c: CredentialDTO) {
  await store.setCredentialEnabled(store.credChannel, c.index, !c.enabled)
}

async function setAllCreds(enabled: boolean) {
  for (const c of [...store.credentials]) {
    if (c.enabled !== enabled) await store.setCredentialEnabled(store.credChannel, c.index, enabled)
  }
}

// ---- ChatGPT 账号授权（Codex，0.2.21）----
// 桌面增强：本机 1455 回环监听 → 浏览器授权完成后轮询自动绑定；
// 回环不可用（端口占用）时用手动粘贴回调兜底；Token/JSON 通道直接导入。
const codexTab = ref<'oauth' | 'token'>('oauth')
const codex = ref({
  sessionId: '',
  authUrl: '',
  desktopUrl: '',
  listening: false,
  pasted: '',
  importText: '',
  busy: false,
  error: '',
  done: false,
  display: '',
  channelID: '', // '' = 新建 Codex 渠道；非空 = 更新该渠道凭证
  targetLabel: '',
})
let codexTimer: number | undefined

function stopCodexPoll() {
  if (codexTimer !== undefined) {
    window.clearInterval(codexTimer)
    codexTimer = undefined
  }
}

function openCodexAuth() {
  stopCodexPoll()
  codex.value = {
    sessionId: '',
    authUrl: '',
    desktopUrl: '',
    listening: false,
    pasted: '',
    importText: '',
    busy: false,
    error: '',
    done: false,
    display: '',
    channelID: form.value.id,
    targetLabel: form.value.id
      ? `绑定到「${form.value.name || form.value.id}」`
      : '将新建一条 Codex 渠道',
  }
  codexTab.value = 'oauth'
  view.value = 'codex'
}

// 发起授权：拿授权链接 → 打开系统浏览器 → 开始轮询等待（回环自动收码）
async function startCodexAuth() {
  codex.value.busy = true
  codex.value.error = ''
  try {
    const info = await bridge().app.StartCodexOAuth()
    if (!info) return
    Object.assign(codex.value, {
      sessionId: info.sessionId,
      authUrl: info.authUrl,
      desktopUrl: info.desktopUrl,
      listening: info.listening,
    })
    openExternal(info.desktopUrl)
    stopCodexPoll()
    codexTimer = window.setInterval(() => void pollCodex(), 1200)
  } catch (e) {
    codex.value.error = errText(e)
  } finally {
    codex.value.busy = false
  }
}

// 轮询授权状态：done 即自动绑定（凭证不出后端，摘要回填展示）
async function pollCodex() {
  if (!codex.value.sessionId || codex.value.done) return
  try {
    const st = await bridge().app.PollCodexOAuth(codex.value.sessionId)
    if (!st) return
    if (st.state === 'done') {
      await bindCodex('')
    } else if (st.state === 'error') {
      stopCodexPoll()
      codex.value.error = st.error || '授权失败'
    }
  } catch {
    // 轮询单次失败静默重试（下一次 tick 继续）；持续失败由用户重新发起
  }
}

async function bindCodex(codeOrURL: string) {
  codex.value.busy = true
  codex.value.error = ''
  try {
    const res = await bridge().app.BindCodexOAuth(
      codex.value.sessionId,
      codex.value.channelID,
      form.value.name,
      codeOrURL,
    )
    if (!res) return
    stopCodexPoll()
    codex.value.done = true
    codex.value.display = res.display
    // 绑定到新建渠道：表单切到该渠道（用户可继续改模型名/备注）
    if (res.created) {
      editingID.value = res.channelId
      form.value.id = res.channelId
      if (!form.value.name) form.value.name = res.name
    }
    boundAccount.value = res.display
    await store.load()
  } catch (e) {
    stopCodexPoll()
    codex.value.error = errText(e)
  } finally {
    codex.value.busy = false
  }
}

// 手动粘贴回调兜底：完整 URL / query 串 / 裸 code 都接受（后端解析并校验 state）
async function submitCodexPaste() {
  const v = codex.value.pasted.trim()
  if (!v) return
  await bindCodex(v)
}

// Token / JSON 通道：auth.json / access_token / refresh_token（后端识别 + 必要时刷新）
async function importCodex() {
  codex.value.busy = true
  codex.value.error = ''
  try {
    const res = await bridge().app.ImportCodexCredential(
      codex.value.channelID,
      form.value.name,
      codex.value.importText,
    )
    if (!res) return
    codex.value.done = true
    codex.value.display = res.display
    if (res.created) {
      editingID.value = res.channelId
      form.value.id = res.channelId
      if (!form.value.name) form.value.name = res.name
    }
    boundAccount.value = res.display
    await store.load()
  } catch (e) {
    codex.value.error = errText(e)
  } finally {
    codex.value.busy = false
  }
}

async function copyAuthUrl() {
  // 授权页复制失败走表单错误位（codex 视图没有 toast 位）——onFail 接管默认提示
  await copy(codex.value.desktopUrl, { onFail: () => (codex.value.error = '复制失败：当前环境剪贴板不可用') })
}

function backToForm() {
  stopCodexPoll()
  view.value = 'form'
}

// ---- 网络代理（0.2.22）----
// 国内网络访问 ChatGPT 必须经代理：授权与对话共用这一出口配置。
const proxyText = ref('')
const proxyInfo = ref<{ ip: string; country: string } | null>(null)
const proxyBusy = ref(false)
const proxyMsg = ref('')

async function loadProxy() {
  try {
    proxyText.value = (await bridge().app.GetProxy()) ?? ''
  } catch {
    // 读取失败不阻断面板（用户仍可重新填写保存）
  }
}

async function saveProxy() {
  proxyBusy.value = true
  proxyMsg.value = ''
  proxyInfo.value = null
  try {
    await bridge().app.SetProxy(proxyText.value.trim())
    proxyMsg.value = proxyText.value.trim() ? '已保存：授权与对话都将经该代理' : '已保存：恢复直连'
  } catch (e) {
    proxyMsg.value = errText(e)
  } finally {
    proxyBusy.value = false
  }
}

// 检查出口：显示代理的实际出口 IP 与地区（判断节点是否被上游支持）
async function checkProxy() {
  proxyBusy.value = true
  proxyMsg.value = ''
  proxyInfo.value = null
  try {
    const info = await bridge().app.CheckProxy(proxyText.value.trim())
    if (info) proxyInfo.value = info
  } catch (e) {
    proxyMsg.value = errText(e)
  } finally {
    proxyBusy.value = false
  }
}

// 打开内部日志目录（0.0.09）：排障时按时间翻当天日志——"卡在哪一层"一眼可见
async function openLogDir() {
  try {
    await bridge().app.OpenLogDir()
  } catch (e) {
    toast('error', errText(e))
  }
}

onBeforeUnmount(stopCodexPoll)

onMounted(async () => {
  await store.load()
  await store.loadPresets()
  await loadProxy()
})
</script>

<template>
  <BaseModal open title="模型渠道" panel-class="w-full max-w-[820px]" @close="emit('close')">
    <!-- 错误条：单条即可，不堆叠 -->
    <div
      v-if="store.error"
      class="mx-5 mt-4 rounded-xl border border-[var(--c-err)] bg-[var(--c-err-soft)] px-3 py-2 text-xs text-[var(--c-err-text)]"
    >
      {{ store.error }}
    </div>

    <!-- 网络代理（0.2.22）：国内访问 ChatGPT 必须经代理；授权与对话共用此出口 -->
    <div class="mx-5 mt-4 rounded-xl border border-[var(--c-border)] px-3 py-2">
      <div class="flex flex-wrap items-center gap-2">
        <span class="shrink-0 text-xs text-[var(--c-text-dim)]">网络代理</span>
        <input
          v-model="proxyText"
          placeholder="http://127.0.0.1:7897（国内访问 ChatGPT 必需；留空 = 直连）"
          class="min-w-0 flex-1 rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-1.5 text-xs"
        />
        <button class="chip shrink-0" :disabled="proxyBusy" @click="saveProxy">保存</button>
        <button class="chip shrink-0" :disabled="proxyBusy" title="探测出口 IP 与地区" @click="checkProxy">
          检查出口
        </button>
      </div>
      <p
        v-if="proxyMsg || proxyInfo"
        class="mt-1 text-xs"
        :class="proxyInfo ? 'text-[var(--c-ok-text)]' : 'text-[var(--c-text-dim)]'"
      >
        <template v-if="proxyInfo">✓ 出口 {{ proxyInfo.ip }}（地区 {{ proxyInfo.country }}）</template>
        <template v-else>{{ proxyMsg }}</template>
      </p>
    </div>

    <!-- 诊断日志（0.0.09）：内部日志按天记录轮次/上游/看门狗事件——
         遇到"卡住/没反应"先打开这里按时间找最后一行 -->
    <div class="mx-5 mt-3 flex flex-wrap items-center gap-2 rounded-xl border border-[var(--c-border)] px-3 py-2">
      <span class="shrink-0 text-xs text-[var(--c-text-dim)]">诊断日志</span>
      <span class="min-w-0 flex-1 text-[11px] text-[var(--c-text-faint)]">
        每次对话的上游请求、超时与工具执行都留有记录（保留 7 天）
      </span>
      <button class="chip shrink-0" @click="openLogDir">打开日志文件夹</button>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto p-5">
      <!-- ================= 视图 1：渠道列表 ================= -->
      <div v-if="view === 'list'" class="space-y-2">
        <div v-if="store.loading" class="py-8 text-center text-sm text-[var(--c-text-dim)]">加载中…</div>
        <div
          v-else-if="!store.list.length"
          class="rounded-xl border border-dashed border-[var(--c-border)] py-10 text-center"
        >
          <p class="text-sm text-[var(--c-text-dim)]">还没有渠道</p>
          <p class="mt-1 text-xs text-[var(--c-text-faint)]">添加一个网关即可开始对话</p>
        </div>

        <div
          v-for="ch in store.list"
          :key="ch.id"
          class="rounded-xl border px-4 py-3"
          :class="
            ch.active
              ? 'border-[var(--c-primary)] bg-[var(--c-primary-soft)]'
              : 'border-[var(--c-border)] bg-[var(--c-surface)]'
          "
        >
          <div class="flex items-center gap-3">
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-2">
                <span class="truncate text-sm font-medium">{{ ch.name }}</span>
                <span v-if="ch.active" class="stat px-2 py-0.5 text-xs text-[var(--c-primary)]">默认</span>
                <span class="stat px-2 py-0.5 text-xs">{{ ch.protocol }}</span>
                <!-- 健康读数（0.0.23）：故障切换是自动的，这一列让切换可见。
                     successRate=-1（近 7 天无请求）写"暂无数据"，不编 100%。 -->
                <span
                  class="stat px-2 py-0.5 text-xs"
                  :class="healthTone(ch.id)"
                  :title="healthTitle(ch.id)"
                >
                  {{ healthLabel(ch.id) }}
                </span>
                <button
                  v-if="ch.credentialCount || ch.hasKey"
                  class="stat cursor-pointer px-2 py-0.5 text-xs transition-colors hover:border-[var(--c-primary)] hover:text-[var(--c-primary)]"
                  :class="ch.credentialDisabled ? 'border-[var(--c-warn)] text-[var(--c-warn-text)]' : ''"
                  title="管理凭证（查看/禁用/启用单条 Key）"
                  @click="openCredentials(ch.id)"
                >
                  {{ credBadge(ch) }}
                </button>
                <span v-else class="stat border-[var(--c-warn)] px-2 py-0.5 text-xs text-[var(--c-warn-text)]">
                  未配置凭证
                </span>
                <span
                  v-if="authLabel(ch.auth)"
                  class="stat px-2 py-0.5 text-xs"
                  title="渠道级鉴权方式（覆盖协议默认）"
                  >鉴权 {{ authLabel(ch.auth) }}</span
                >
                <span
                  class="stat px-2 py-0.5 text-xs"
                  :class="
                    ch.status === 'auto_disabled'
                      ? 'border-[var(--c-err)] text-[var(--c-err-text)]'
                      : ch.status === 'manually_disabled'
                        ? 'text-[var(--c-text-faint)]'
                        : 'text-[var(--c-ok-text)]'
                  "
                  >{{ statusLabel(ch) }}</span
                >
              </div>
              <div class="mt-1 truncate text-xs text-[var(--c-text-faint)]">
                {{ (ch.models?.length ? ch.models : [ch.model]).filter(Boolean).join(' / ') || '未填模型' }}
                · {{ ch.baseUrl || '默认地址' }}
              </div>
              <!-- 测试结果：✓ 延迟+回显 / ✗ 失败原因（就地显示，不必翻日志） -->
              <div
                v-if="store.testResults[ch.id]"
                class="mt-1 truncate text-xs"
                :class="
                  store.testResults[ch.id].ok ? 'text-[var(--c-ok-text)]' : 'text-[var(--c-err-text)]'
                "
              >
                <template v-if="store.testResults[ch.id].ok">
                  ✓ {{ store.testResults[ch.id].ms }}ms · {{ store.testResults[ch.id].model }}
                  <template v-if="store.testResults[ch.id].reply">· {{ store.testResults[ch.id].reply }}</template>
                </template>
                <template v-else>✗ {{ store.testResults[ch.id].error }}</template>
              </div>
            </div>
            <button
              class="chip shrink-0"
              :disabled="store.busy || store.testing === ch.id"
              title="发一条最小请求验证连通性（不产生副作用）"
              @click="store.test(ch.id)"
            >
              {{ store.testing === ch.id ? '测试中…' : '测试' }}
            </button>
            <button
              v-if="!ch.active && ch.status === 'enabled'"
              class="chip shrink-0"
              :disabled="store.busy"
              @click="store.activate(ch.id)"
            >
              设为默认
            </button>
            <button class="chip shrink-0" :disabled="store.busy" @click="startEdit(ch)">编辑</button>
            <button class="chip shrink-0" :disabled="store.busy" title="复制为新渠道" @click="duplicate(ch)">
              复制
            </button>
            <button
              class="chip shrink-0 text-[var(--c-err-text)]"
              :disabled="store.busy || ch.active"
              :title="ch.active ? '正在使用的渠道不能删除，请先把别的渠道设为默认' : '删除渠道'"
              @click="remove(ch)"
            >
              删除
            </button>
          </div>
        </div>

        <button class="btn-primary mt-2 w-full gap-2 py-2.5 text-sm" @click="startCreate">
          <AppIcon name="plus" :size="14" /> 新增渠道
        </button>
      </div>

      <!-- ================= 视图 2：新增/编辑表单（四分区） ================= -->
      <div v-else-if="view === 'form'" class="space-y-4">
        <!-- ① 基本信息 -->
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <label class="block sm:col-span-2">
            <span class="mb-1 block text-xs text-[var(--c-text-dim)]">快速模板</span>
            <select
              class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
              @change="applyPreset(($event.target as HTMLSelectElement).value)"
            >
              <option value="">— 选择服务商（自动填充地址与模型）—</option>
              <option v-for="p in store.presets" :key="p.key" :value="p.key">{{ p.name }}</option>
            </select>
          </label>

          <label class="block">
            <span class="mb-1 block text-xs text-[var(--c-text-dim)]">协议类型</span>
            <select
              v-model="form.protocol"
              class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
            >
              <option value="openai">OpenAI 兼容（/chat/completions）</option>
              <option value="anthropic">Anthropic Messages（/v1/messages）</option>
              <option value="codex">OpenAI Codex（ChatGPT 订阅账号 /responses）</option>
            </select>
          </label>

          <label class="block">
            <span class="mb-1 block text-xs text-[var(--c-text-dim)]">名称</span>
            <input
              v-model="form.name"
              class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
              placeholder="例如 主渠道"
            />
          </label>

          <label class="block sm:col-span-2">
            <span class="mb-1 block text-xs text-[var(--c-text-dim)]">网关地址（{{ protocolHint }}）</span>
            <input
              v-model="form.baseUrl"
              class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
              placeholder="https://api.deepseek.com/v1"
            />
          </label>
        </div>

        <!-- ② 凭证 -->
        <div class="border-t border-[var(--c-border)] pt-3">
          <label class="block">
            <span class="mb-1 block text-xs text-[var(--c-text-dim)]">
              凭证{{ editingID ? '（留空保持原凭证）' : '' }} · 多个 Key 每行一个（轮询负载均衡、失败自动禁用）
            </span>
            <textarea
              v-model="form.apiKey"
              rows="2"
              autocomplete="off"
              class="w-full resize-none rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 font-mono text-sm"
              :placeholder="editingID ? '已配置，留空则不变' : 'sk-…（多个则换行分隔）'"
            ></textarea>
          </label>
          <div class="mt-2 flex flex-wrap items-center gap-2">
            <button
              class="chip border-[var(--c-primary)] text-[var(--c-primary)]"
              type="button"
              title="ChatGPT 订阅账号 OAuth 授权，或粘贴 auth.json / token"
              @click="openCodexAuth"
            >
              {{ boundAccount ? `已绑定 ${boundAccount} · 换号/重新授权` : '用 ChatGPT 账号授权 / 粘贴 Token' }}
            </button>
            <button
              v-if="editingID && form.id"
              class="chip"
              type="button"
              @click="openCredentials(form.id, 'form')"
            >
              管理已存凭证（查看/禁用/启用）
            </button>
          </div>

          <!-- 鉴权方式（0.2.20）：覆盖协议默认，兼容各种中转/自建网关的鉴权形态 -->
          <div class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label class="block">
              <span class="mb-1 block text-xs text-[var(--c-text-dim)]">鉴权方式</span>
              <select
                v-model="form.authType"
                class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
              >
                <option value="default">跟随协议默认（{{ protocolDefaultAuth }}）</option>
                <option value="bearer">Bearer 令牌（Authorization: Bearer）</option>
                <option value="header">自定义请求头</option>
                <option value="query">URL 查询参数</option>
                <option value="none">无鉴权（前置代理已鉴权）</option>
              </select>
            </label>
            <template v-if="form.authType === 'header' || form.authType === 'query'">
              <label class="block">
                <span class="mb-1 block text-xs text-[var(--c-text-dim)]">
                  {{ form.authType === 'header' ? '请求头名称' : 'URL 参数名称' }}
                </span>
                <input
                  v-model="form.authName"
                  class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 font-mono text-sm"
                  :placeholder="form.authType === 'header' ? 'api-key' : 'key'"
                />
              </label>
              <label class="block sm:col-span-2">
                <span class="mb-1 block text-xs text-[var(--c-text-dim)]">
                  值模板（支持 {api_key} 占位符；留空 = 仅凭证本身）
                </span>
                <input
                  v-model="form.authValue"
                  class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 font-mono text-sm"
                  placeholder="Token {api_key}"
                />
              </label>
            </template>
          </div>
        </div>

        <!-- ③ 模型与路由 -->
        <div class="border-t border-[var(--c-border)] pt-3">
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <label class="block sm:col-span-2">
              <span class="mb-1 block text-xs text-[var(--c-text-dim)]">模型（每行一个；首个为默认）</span>
              <textarea
                v-model="form.modelsText"
                rows="2"
                class="w-full resize-none rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 font-mono text-sm"
                placeholder="deepseek-chat"
              ></textarea>
              <span class="mt-1 flex items-center gap-2">
                <button class="chip" :disabled="store.busy || !form.baseUrl" @click="discover">
                  <AppIcon name="refresh" :size="13" /> 同步模型
                </button>
                <span v-if="store.models.length" class="text-xs text-[var(--c-text-faint)]">
                  已获取 {{ store.models.length }} 个模型，已填入上方可删减
                </span>
              </span>
            </label>

            <label class="block">
              <span class="mb-1 block text-xs text-[var(--c-text-dim)]">优先级（越大越优先）</span>
              <input
                v-model.number="form.priority"
                type="number"
                min="0"
                class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
              />
            </label>

            <label class="block">
              <span class="mb-1 block text-xs text-[var(--c-text-dim)]">权重（同优先级内加权随机，0 = 默认）</span>
              <input
                v-model.number="form.weight"
                type="number"
                min="0"
                class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
              />
            </label>

            <label class="block">
              <span class="mb-1 block text-xs text-[var(--c-text-dim)]">
                上下文上限（token，0 = 不限；本地折叠用，不发上游）
              </span>
              <input
                v-model.number="form.contextLimit"
                type="number"
                min="0"
                placeholder="例如 128000"
                class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
              />
              <span class="mt-1 block text-[11px] text-[var(--c-text-faint)]">
                估算口径：4 个 ASCII 字符 ≈ 1 token、1 个非 ASCII 字符 ≈ 1 token（保守上界）。
                接近上限时按顺序折叠：旧图片 → 两轮以前的命令/写入回执 → 更早的只读结果。
              </span>
            </label>

            <div class="grid grid-cols-2 gap-2">
              <label class="block">
                <span class="mb-1 block text-xs text-[var(--c-text-dim)]">输入单价 ($/1M tok)</span>
                <input
                  v-model.number="form.priceIn"
                  type="number"
                  min="0"
                  step="0.01"
                  placeholder="0 = 未配置"
                  class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
                />
              </label>
              <label class="block">
                <span class="mb-1 block text-xs text-[var(--c-text-dim)]">输出单价 ($/1M tok)</span>
                <input
                  v-model.number="form.priceOut"
                  type="number"
                  min="0"
                  step="0.01"
                  placeholder="0 = 未配置"
                  class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
                />
              </label>
            </div>
            <span class="mt-1 block text-[11px] text-[var(--c-text-faint)]">
              可选。配置后「统计」面板按当前渠道单价估算成本；多渠道混用时为近似值。
            </span>

            <label class="block">
              <span class="mb-1 block text-xs text-[var(--c-text-dim)]">状态</span>
              <select
                v-model="form.status"
                class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
              >
                <option value="enabled">启用</option>
                <option value="manually_disabled">停用（不参与选路）</option>
                <option v-if="form.status === 'auto_disabled'" value="auto_disabled">
                  已自动禁用（改为启用可恢复）
                </option>
              </select>
            </label>

            <label class="flex items-end gap-2 pb-1">
              <input v-model="form.autoBan" type="checkbox" class="h-4 w-4 accent-[var(--c-primary)]" />
              <span class="text-xs text-[var(--c-text-dim)]">
                故障自动禁用（上游 429/5xx 或鉴权失败时自动摘除该渠道/Key）
              </span>
            </label>
          </div>
        </div>

        <!-- ④ 高级（折叠） -->
        <details class="rounded-xl border border-[var(--c-border)] px-3 py-2">
          <summary class="cursor-pointer text-xs text-[var(--c-text-dim)]">
            高级设置（模型映射 / 参数与请求头覆写，通常留空）
          </summary>
          <div class="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label class="block sm:col-span-2">
              <span class="mb-1 block text-xs text-[var(--c-text-dim)]">
                模型映射（每行 下游模型=上游真实模型；把请求改写成上游实际名称）
              </span>
              <textarea
                v-model="form.mappingText"
                rows="2"
                class="w-full resize-none rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 font-mono text-sm"
                placeholder="deepseek-chat=deepseek-v3"
              ></textarea>
            </label>
            <label class="block">
              <span class="mb-1 block text-xs text-[var(--c-text-dim)]">请求头覆写（每行 名称=值）</span>
              <textarea
                v-model="form.headerText"
                rows="2"
                class="w-full resize-none rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 font-mono text-sm"
                placeholder="x-custom-header=value"
              ></textarea>
            </label>
            <label class="block">
              <span class="mb-1 block text-xs text-[var(--c-text-dim)]">参数覆写（JSON 对象，合并进请求体）</span>
              <textarea
                v-model="form.paramText"
                rows="2"
                class="w-full resize-none rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 font-mono text-sm"
                placeholder='{"temperature": 0.1}'
              ></textarea>
            </label>
          </div>
        </details>

        <div class="flex justify-end gap-2 pt-2">
          <button class="chip" @click="backToList">取消</button>
          <button class="btn-primary px-5 py-2 text-sm" :disabled="store.busy" @click="save">
            {{ store.busy ? '保存中…' : '保存' }}
          </button>
        </div>
      </div>

      <!-- ================= 视图 4：ChatGPT 账号授权（Codex） ================= -->
      <div v-else-if="view === 'codex'" class="space-y-4">
        <div class="flex items-center gap-2">
          <button class="chip shrink-0" @click="backToForm">← 返回</button>
          <div class="min-w-0 flex-1 truncate text-sm font-medium">ChatGPT 账号绑定</div>
          <span class="stat shrink-0 px-2 py-0.5 text-xs">{{ codex.targetLabel }}</span>
        </div>

        <!-- 通道切换：OAuth 授权 / Token·JSON 粘贴 -->
        <div class="flex gap-1 rounded-xl bg-[var(--c-surface-soft)] p-1 text-xs">
          <button
            class="flex-1 rounded-lg py-1.5 transition-colors"
            :class="codexTab === 'oauth' ? 'bg-[var(--c-surface)] font-medium shadow-sm' : 'text-[var(--c-text-dim)]'"
            @click="codexTab = 'oauth'"
          >
            OAuth 授权
          </button>
          <button
            class="flex-1 rounded-lg py-1.5 transition-colors"
            :class="codexTab === 'token' ? 'bg-[var(--c-surface)] font-medium shadow-sm' : 'text-[var(--c-text-dim)]'"
            @click="codexTab = 'token'"
          >
            Token / JSON
          </button>
        </div>

        <!-- OAuth 授权通道 -->
        <div v-if="codexTab === 'oauth'" class="space-y-3">
          <p class="text-xs text-[var(--c-text-dim)]">
            在浏览器中登录 ChatGPT（订阅账号）完成授权。
            {{
              codex.listening
                ? '本机已监听回调端口：授权完成后将自动绑定，无需手动操作。'
                : '回调端口被占用：授权后请把浏览器地址栏的回调地址粘贴到下方。'
            }}
          </p>
          <button
            class="btn-primary w-full gap-2 py-2.5 text-sm"
            :disabled="codex.busy || codex.done"
            @click="startCodexAuth"
          >
            {{ codex.busy && !codex.sessionId ? '准备中…' : codex.sessionId ? '重新打开授权页' : '在浏览器中打开授权页' }}
          </button>

          <div v-if="codex.sessionId" class="space-y-2">
            <div class="flex items-center gap-2">
              <input
                readonly
                :value="codex.authUrl"
                class="min-w-0 flex-1 truncate rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 font-mono text-[11px]"
              />
              <button class="chip shrink-0" @click="copyAuthUrl">复制链接</button>
            </div>
            <div class="flex items-center gap-2">
              <input
                v-model="codex.pasted"
                :disabled="codex.done"
                placeholder="粘贴回调地址或授权码（自动完成失败时使用）"
                class="min-w-0 flex-1 rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-xs"
              />
              <button
                class="chip shrink-0"
                :disabled="codex.busy || codex.done || !codex.pasted.trim()"
                @click="submitCodexPaste"
              >
                我已授权，继续
              </button>
            </div>
            <p
              class="text-xs"
              :class="
                codex.error ? 'text-[var(--c-err-text)]' : codex.done ? 'text-[var(--c-ok-text)]' : 'text-[var(--c-text-faint)]'
              "
            >
              {{ codex.error || (codex.done ? `✓ 已绑定 ${codex.display}` : '等待授权完成…（授权成功后此处自动更新）') }}
            </p>
          </div>
        </div>

        <!-- Token / JSON 粘贴通道 -->
        <div v-else class="space-y-3">
          <p class="text-xs text-[var(--c-text-dim)]">
            粘贴 Codex 的 auth.json 全文、access_token（at-…）或 refresh_token；
            系统会自动识别、必要时用 refresh_token 换取新凭证。
          </p>
          <textarea
            v-model="codex.importText"
            rows="5"
            autocomplete="off"
            class="w-full resize-none rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 font-mono text-xs"
            placeholder='{"tokens":{"access_token":"…","refresh_token":"…"}} 或直接粘贴 rt-… / access_token'
          ></textarea>
          <button
            class="btn-primary w-full py-2.5 text-sm"
            :disabled="codex.busy || !codex.importText.trim()"
            @click="importCodex"
          >
            {{ codex.busy ? '校验中…' : '校验并绑定' }}
          </button>
          <p v-if="codex.error" class="text-xs text-[var(--c-err-text)]">{{ codex.error }}</p>
          <p v-else-if="codex.done" class="text-xs text-[var(--c-ok-text)]">✓ 已绑定 {{ codex.display }}</p>
          <p v-else class="text-xs text-[var(--c-text-faint)]">
            提示：Codex CLI 的凭证文件位于 ~/.codex/auth.json（可直接整文件粘贴）。
          </p>
        </div>
      </div>

      <!-- ================= 视图 3：凭证管理 ================= -->
      <div v-else class="space-y-3">
        <div class="flex items-center gap-2">
          <button class="chip shrink-0" @click="credBack">← 返回</button>
          <div class="min-w-0 flex-1 truncate text-sm font-medium">{{ credChannelName }}</div>
          <span class="stat shrink-0 px-2 py-0.5 text-xs">
            {{ store.credentials.length }} 条<template v-if="credDisabledCount">
              · {{ credDisabledCount }} 禁用</template
            >
          </span>
        </div>

        <div
          v-for="c in store.credentials"
          :key="c.index"
          class="flex items-center gap-3 rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] px-3 py-2"
        >
          <span class="w-8 shrink-0 text-xs text-[var(--c-text-faint)]">#{{ c.index + 1 }}</span>
          <span class="min-w-0 flex-1 truncate font-mono text-xs">{{ c.preview }}</span>
          <span
            class="stat shrink-0 px-2 py-0.5 text-xs"
            :class="
              c.enabled ? 'text-[var(--c-ok-text)]' : 'border-[var(--c-err)] text-[var(--c-err-text)]'
            "
            >{{ c.enabled ? '启用' : '已禁用' }}</span
          >
          <button class="chip shrink-0" :disabled="store.busy" @click="toggleCred(c)">
            {{ c.enabled ? '禁用' : '启用' }}
          </button>
        </div>

        <div class="flex gap-2 pt-1">
          <button class="chip" :disabled="store.busy" @click="setAllCreds(true)">全部启用</button>
          <button class="chip" :disabled="store.busy" @click="setAllCreds(false)">全部禁用</button>
        </div>
        <p class="text-xs text-[var(--c-text-faint)]">
          提示：全部凭证不可用时该渠道自动禁用（不参与选路）；启用任一凭证即自动恢复。
        </p>
      </div>
    </div>
  </BaseModal>
</template>
