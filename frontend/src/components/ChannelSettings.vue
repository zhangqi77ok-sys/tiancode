<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useChannelStore } from '../stores/channels'
import { useDialogs } from '../composables/useDialogs'
import type { ChannelDTO } from '../wails'
import AppIcon from './AppIcon.vue'
import BaseModal from './BaseModal.vue'

// 渠道设置面板：列表 + 表单两态，宿主为 BaseModal（Esc/焦点陷阱/遮罩统一处理）。
// 交互纪律：保存/删除/切换后以后端返回的列表为准（不做乐观更新）；
// 密钥输入框永远留空并提示"留空保持原密钥"——密钥不回显是安全约束，不是偷懒。
const emit = defineEmits<{ (e: 'close'): void }>()
const store = useChannelStore()
const dialogs = useDialogs()

const showForm = ref(false)
const editingID = ref('')
const form = ref({ id: '', name: '', protocol: 'openai', baseUrl: '', model: '', apiKey: '' })

function reset() {
  store.clearError()
  store.models = []
  editingID.value = ''
  form.value = { id: '', name: '', protocol: 'openai', baseUrl: '', model: '', apiKey: '' }
}

function startCreate() {
  reset()
  showForm.value = true
}

function startEdit(ch: ChannelDTO) {
  reset()
  editingID.value = ch.id
  form.value = { id: ch.id, name: ch.name, protocol: ch.protocol, baseUrl: ch.baseUrl, model: ch.model, apiKey: '' }
  showForm.value = true
}

function applyPreset(key: string) {
  const p = store.presets.find((x) => x.key === key)
  if (!p) return
  form.value.protocol = p.protocol
  if (p.baseUrl) form.value.baseUrl = p.baseUrl
  if (p.suggestedModel) form.value.model = p.suggestedModel
  if (!form.value.name) form.value.name = p.name
}

async function save() {
  await store.save(form.value)
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
              <span
                class="stat px-2 py-0.5 text-xs"
                :class="ch.hasKey ? '' : 'border-[var(--c-warn)] text-[var(--c-warn-text)]'"
              >
                {{ ch.hasKey ? '已配置密钥' : '未配置密钥' }}
              </span>
            </div>
            <div class="mt-1 truncate text-xs text-[var(--c-text-faint)]">{{ ch.baseUrl }} · {{ ch.model }}</div>
          </div>
          <button v-if="!ch.active" class="chip shrink-0" :disabled="store.busy" @click="store.activate(ch.id)">
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
            <span class="mb-1 block text-xs text-[var(--c-text-dim)]">名称</span>
            <input
              v-model="form.name"
              class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
              placeholder="例如 主渠道"
            />
          </label>

          <label class="block">
            <span class="mb-1 block text-xs text-[var(--c-text-dim)]">API Key{{ editingID ? '（留空保持原密钥）' : '' }}</span>
            <input
              v-model="form.apiKey"
              type="password"
              autocomplete="off"
              class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
              :placeholder="editingID ? '已配置，留空则不变' : 'sk-…'"
            />
          </label>

          <label class="block sm:col-span-2">
            <span class="mb-1 block text-xs text-[var(--c-text-dim)]">网关地址（含 /v1）</span>
            <input
              v-model="form.baseUrl"
              class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
              placeholder="https://api.deepseek.com/v1"
            />
          </label>

          <label class="block sm:col-span-2">
            <span class="mb-1 block text-xs text-[var(--c-text-dim)]">模型</span>
            <div class="flex gap-2">
              <input
                v-model="form.model"
                list="model-options"
                class="min-w-0 flex-1 rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
                placeholder="deepseek-chat"
              />
              <datalist id="model-options">
                <option v-for="m in store.models" :key="m" :value="m" />
              </datalist>
              <button class="chip shrink-0" :disabled="store.busy || !form.baseUrl" @click="store.discover(form)">
                <AppIcon name="refresh" :size="13" /> 同步模型
              </button>
            </div>
            <span v-if="store.models.length" class="mt-1 block text-xs text-[var(--c-text-faint)]">
              已获取 {{ store.models.length }} 个模型，点击输入框可选择
            </span>
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
