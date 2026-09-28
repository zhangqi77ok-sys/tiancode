<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useChannelStore } from '../stores/channels'
import { useDialogs } from '../composables/useDialogs'
import type { ChannelDTO, CredentialDTO } from '../wails'
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

const view = ref<'list' | 'form' | 'keys'>('list')
const editingID = ref('')

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
})
const form = ref(emptyForm())

// 协议默认地址提示（选 anthropic 时给用户可照抄的地址）
const protocolHint = computed(() =>
  form.value.protocol === 'anthropic'
    ? 'Anthropic Messages 协议：地址形如 https://api.anthropic.com/v1'
    : 'OpenAI 兼容协议：地址形如 https://api.deepseek.com/v1',
)

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
  }
}

function startEdit(ch: ChannelDTO) {
  reset()
  editingID.value = ch.id
  fillFrom(ch)
  view.value = 'form'
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

onMounted(async () => {
  await store.load()
  await store.loadPresets()
})
</script>

<template>
  <BaseModal open title="模型渠道" panel-class="w-[820px] max-md:w-full" @close="emit('close')">
    <!-- 错误条：单条即可，不堆叠 -->
    <div
      v-if="store.error"
      class="mx-5 mt-4 rounded-xl border border-[var(--c-err)] bg-[var(--c-err-soft)] px-3 py-2 text-xs text-[var(--c-err-text)]"
    >
      {{ store.error }}
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
                {{ ch.baseUrl || '默认地址' }} · {{ (ch.models?.length ? ch.models : [ch.model]).join(' / ') }} ·
                优先级 {{ ch.priority ?? 100 }} · 权重 {{ ch.weight || '默认' }}
                <template v-if="ch.autoBan"> · 故障自动禁用</template>
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
          <button
            v-if="editingID && form.id"
            class="chip mt-2"
            type="button"
            @click="openCredentials(form.id, 'form')"
          >
            管理已存凭证（查看/禁用/启用）
          </button>
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
