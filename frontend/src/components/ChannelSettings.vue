<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useChannelStore } from '../stores/channels'
import { useDialogs } from '../composables/useDialogs'
import type { ChannelDTO } from '../wails'
import AppIcon from './AppIcon.vue'
import BaseModal from './BaseModal.vue'

// 渠道设置面板：列表 + 表单两态，宿主为 BaseModal（Esc/焦点陷阱/遮罩统一处理）。
// 交互纪律：保存/删除/切换后以后端返回的列表为准（不做乐观更新）；
// 凭证输入框永远留空并提示"留空保持原凭证"——凭证不回显是安全约束。
// 0.2.17：表单对齐多协议渠道层——协议类型、多凭证（换行分隔）、多模型、优先级/权重、启停。
const emit = defineEmits<{ (e: 'close'): void }>()
const store = useChannelStore()
const dialogs = useDialogs()

const showForm = ref(false)
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
})
const form = ref(emptyForm())

// 协议默认地址提示（选 anthropic 时给用户可照抄的地址）
const protocolHint = computed(() =>
  form.value.protocol === 'anthropic'
    ? 'Anthropic Messages 协议：地址形如 https://api.anthropic.com/v1'
    : 'OpenAI 兼容协议：地址形如 https://api.deepseek.com/v1',
)

function reset() {
  store.clearError()
  store.models = []
  editingID.value = ''
  form.value = emptyForm()
}

function startCreate() {
  reset()
  showForm.value = true
}

function startEdit(ch: ChannelDTO) {
  reset()
  editingID.value = ch.id
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
    status: ch.status === 'manually_disabled' ? 'manually_disabled' : ch.status === 'auto_disabled' ? 'auto_disabled' : 'enabled',
  }
  showForm.value = true
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
  })
  if (!store.error) {
    showForm.value = false
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
    showForm.value = false
    reset()
  }
}

function statusLabel(ch: ChannelDTO): string {
  if (ch.status === 'auto_disabled') return '已自动禁用'
  if (ch.status === 'manually_disabled') return '已停用'
  return '启用'
}

onMounted(async () => {
  await store.load()
  await store.loadPresets()
})
</script>

<template>
  <BaseModal open title="模型渠道" panel-class="w-[720px] max-md:w-full" @close="emit('close')">
    <!-- 错误条：单条即可，不堆叠 -->
    <div
      v-if="store.error"
      class="mx-5 mt-4 rounded-xl border border-[var(--c-err)] bg-[var(--c-err-soft)] px-3 py-2 text-xs text-[var(--c-err-text)]"
    >
      {{ store.error }}
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto p-5">
      <!-- 渠道列表 -->
      <div v-if="!showForm" class="space-y-2">
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
          class="flex items-center gap-3 rounded-xl border px-4 py-3"
          :class="
            ch.active
              ? 'border-[var(--c-primary)] bg-[var(--c-primary-soft)]'
              : 'border-[var(--c-border)] bg-[var(--c-surface)]'
          "
        >
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="truncate text-sm font-medium">{{ ch.name }}</span>
              <span v-if="ch.active" class="stat px-2 py-0.5 text-xs text-[var(--c-primary)]">默认</span>
              <span class="stat px-2 py-0.5 text-xs">{{ ch.protocol }}</span>
              <span
                class="stat px-2 py-0.5 text-xs"
                :class="ch.hasKey ? '' : 'border-[var(--c-warn)] text-[var(--c-warn-text)]'"
              >
                {{ ch.hasKey ? '已配置凭证' : '未配置凭证' }}
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
            </div>
          </div>
          <button
            v-if="!ch.active && ch.status === 'enabled'"
            class="chip shrink-0"
            :disabled="store.busy"
            @click="store.activate(ch.id)"
          >
            设为默认
          </button>
          <button class="chip shrink-0" :disabled="store.busy" @click="startEdit(ch)">编辑</button>
          <button
            class="chip shrink-0 text-[var(--c-err-text)]"
            :disabled="store.busy || ch.active"
            @click="remove(ch)"
          >
            删除
          </button>
        </div>

        <button class="btn-primary mt-2 w-full gap-2 py-2.5 text-sm" @click="startCreate">
          <AppIcon name="plus" :size="14" /> 新增渠道
        </button>
      </div>

      <!-- 表单 -->
      <div v-else class="space-y-4">
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

          <label class="block sm:col-span-2">
            <span class="mb-1 block text-xs text-[var(--c-text-dim)]">网关地址（{{ protocolHint }}）</span>
            <input
              v-model="form.baseUrl"
              class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
              placeholder="https://api.deepseek.com/v1"
            />
          </label>

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

          <label class="block sm:col-span-2">
            <span class="mb-1 block text-xs text-[var(--c-text-dim)]">状态</span>
            <select
              v-model="form.status"
              class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
            >
              <option value="enabled">启用</option>
              <option value="manually_disabled">停用（不参与选路）</option>
              <option v-if="form.status === 'auto_disabled'" value="auto_disabled">已自动禁用（改为启用可恢复）</option>
            </select>
          </label>
        </div>

        <div class="flex justify-end gap-2 pt-2">
          <button class="chip" @click="showForm = false; reset()">取消</button>
          <button class="btn-primary px-5 py-2 text-sm" :disabled="store.busy" @click="save">
            {{ store.busy ? '保存中…' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </BaseModal>
</template>
