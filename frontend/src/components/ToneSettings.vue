<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useTonesStore } from '../stores/tones'
import BaseModal from './BaseModal.vue'

defineEmits<{ (e: 'close'): void }>()
const tones = useTonesStore()
const error = ref('')
const saved = ref(false)

// 面板里改的是草稿：点保存才落盘。合法性（例如"默认那条不能停用"）只由后端判定，
// 前端不做第二套规则——否则两处规则迟早不一致。
const draft = ref<{ mode: 'fixed' | 'auto'; default: string; disabled: string[] }>({
  mode: 'fixed',
  default: 'plain',
  disabled: [],
})

onMounted(async () => {
  if (!tones.loaded) {
    const msg = await tones.load()
    if (msg) error.value = msg
  }
  draft.value = { mode: tones.mode, default: tones.defaultId, disabled: [...tones.disabled] }
})

const isOff = (id: string) => draft.value.disabled.includes(id)
const defaultName = computed(() => tones.builtin.find((e) => e.id === draft.value.default)?.name ?? draft.value.default)

function setMode(mode: 'fixed' | 'auto') {
  saved.value = false
  draft.value.mode = mode
}

function setDefault(id: string) {
  saved.value = false
  draft.value.default = id
}

// 逐条启停（停用默认那条时后端会拒绝保存并给出原因，界面照实显示那句）
function toggle(id: string) {
  saved.value = false
  draft.value.disabled = isOff(id) ? draft.value.disabled.filter((x) => x !== id) : [...draft.value.disabled, id]
}

async function save() {
  error.value = ''
  saved.value = false
  const msg = await tones.save({
    mode: draft.value.mode,
    default: draft.value.default,
    disabled: draft.value.disabled,
  })
  if (msg) {
    error.value = msg
    return
  }
  saved.value = true
}
</script>

<template>
  <BaseModal open title="语气" panel-class="w-full max-w-[720px]" @close="$emit('close')">
    <p class="px-5 pt-4 text-xs text-[var(--c-text-dim)]">
      语气只决定"怎么回答"，不改变依据：文件内容、命令输出、接口、行号只引用工具结果或用户原文。
    </p>
    <p v-if="error" class="mx-5 mt-3 text-xs text-[var(--c-err-text)]">{{ error }}</p>
    <p v-else-if="saved" class="mx-5 mt-3 text-xs text-[var(--c-ok-text)]">已保存，下一轮对话生效。</p>

    <div class="space-y-3 p-5">
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-xs text-[var(--c-text-dim)]">模式</span>
        <button
          class="chip"
          :class="draft.mode === 'fixed' ? 'border-[var(--c-primary)] text-[var(--c-primary)]' : ''"
          :aria-pressed="draft.mode === 'fixed'"
          @click="setMode('fixed')"
        >
          固定一条
        </button>
        <button
          class="chip"
          :class="draft.mode === 'auto' ? 'border-[var(--c-primary)] text-[var(--c-primary)]' : ''"
          :aria-pressed="draft.mode === 'auto'"
          @click="setMode('auto')"
        >
          按每条消息自动选
        </button>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <span class="text-xs text-[var(--c-text-dim)]">默认</span>
        <select v-model="draft.default" class="field-input max-w-[280px] text-xs" aria-label="默认语气" @change="saved = false">
          <option v-for="e in tones.builtin" :key="e.id" :value="e.id">{{ e.name }}（{{ e.id }}）</option>
        </select>
        <span class="text-[11px] text-[var(--c-text-faint)]">当前默认：{{ defaultName }}</span>
      </div>

      <p class="text-[11px] text-[var(--c-text-faint)]">
        固定：整段回答只按默认那一条。自动：把下面未停用的条目（id / 名称 / 做法）给模型，由它按你最后一条消息选一条；
        你本条明确指定了语气就听你的，没有合适的用默认那条。停用的条目不进名单。
      </p>

      <div class="max-h-[44vh] space-y-1.5 overflow-auto pr-1">
        <div
          v-for="e in tones.builtin"
          :key="e.id"
          class="flex items-start gap-2 rounded-xl border border-[var(--c-border)] px-3 py-2"
        >
          <button
            class="chip shrink-0"
            :class="isOff(e.id) ? '' : 'border-[var(--c-primary)] text-[var(--c-primary)]'"
            :aria-pressed="!isOff(e.id)"
            :title="isOff(e.id) ? '点击启用' : '点击停用'"
            @click="toggle(e.id)"
          >
            {{ isOff(e.id) ? '已停用' : '已启用' }}
          </button>
          <div class="min-w-0 flex-1">
            <div class="truncate text-sm font-medium">
              {{ e.name }}
              <span class="pl-1 text-[11px] font-normal text-[var(--c-text-faint)]">{{ e.id }}</span>
            </div>
            <div class="text-[11px] text-[var(--c-text-dim)]">{{ e.practice }}</div>
          </div>
          <button
            class="chip shrink-0"
            :class="draft.default === e.id ? 'border-[var(--c-primary)] text-[var(--c-primary)]' : ''"
            :aria-pressed="draft.default === e.id"
            title="把这条设为默认"
            @click="setDefault(e.id)"
          >
            {{ draft.default === e.id ? '默认' : '设为默认' }}
          </button>
        </div>
      </div>

      <div class="flex justify-end gap-2">
        <button class="chip" @click="$emit('close')">关闭</button>
        <button class="btn-primary px-4 py-2 text-sm" :disabled="tones.busy" @click="save">
          {{ tones.busy ? '保存中…' : '保存' }}
        </button>
      </div>
    </div>
  </BaseModal>
</template>
