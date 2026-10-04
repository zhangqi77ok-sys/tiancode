<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import BaseModal from './BaseModal.vue'
import AppIcon from './AppIcon.vue'
import { bridge, type SearchHitDTO, type SearchStatsDTO } from '../wails'
import { useChatStore } from '../stores/chat'
import { useWorkspaceStore } from '../stores/workspace'
import { useToast } from '../composables/useToast'
import { errText } from '../composables/errText'

// 跨会话搜索面板（0.0.23）：Ctrl+Shift+F。回答"我上周让模型查过什么来着"——
// 那在另一场会话里，后端扫所有账本（只读、有界），这里只负责输入与呈现。
// 点击命中 → 切会话 + 跳到那一轮（复用 0.0.21 时间线跳转链路，不新造定位）。
//
// 0.0.30 审查 R1：单账本有 4MB 扫描上限，超限的长账本只扫了开头一段——尾部往往
// 正是最近的对话。此前后端把"是否截断"丢了，零命中时本面板说"没有会话里包含这个
// 搜索词"，把"没扫到"说成了"不存在"。现在用后端回传的 stats 如实写明。

const emit = defineEmits<{ (e: 'close'): void }>()
const store = useChatStore()
const ws = useWorkspaceStore()
const { push: toast } = useToast()

const query = ref('')
const scopeWs = ref(false) // 只搜当前工作区（默认全空间——"上周那场"可能在别的项目）
const hits = ref<SearchHitDTO[]>([])
const stats = ref<SearchStatsDTO | null>(null)
const loading = ref(false)
const searched = ref(false)
const error = ref('')
const inputEl = ref<HTMLInputElement | null>(null)

// 本次有多少本长账本因字节上限没扫完（0 = 结果完整，可以断言"不存在"）
const truncatedLedgers = computed(() => stats.value?.truncatedLedgers ?? 0)
// 扫描上限由后端给出，界面不复制一份常量（复制就会漂）
const scanLimitMB = computed(() => Math.round((stats.value?.byteLimitPerLedger ?? 0) / 1024 / 1024))

async function run() {
  const q = query.value.trim()
  if (!q || loading.value) return
  loading.value = true
  error.value = ''
  try {
    const res = await bridge().app.SearchSessions(q, scopeWs.value ? ws.path : '', 50)
    hits.value = res?.hits ?? []
    stats.value = res?.stats ?? null
    searched.value = true
  } catch (e) {
    error.value = errText(e)
    hits.value = []
    stats.value = null
    searched.value = true
  } finally {
    loading.value = false
  }
}

// 点击命中：切到那场会话并定位到所属轮（anchorSeq 缺失的旧数据只切不跳）
async function openHit(h: SearchHitDTO) {
  try {
    await store.selectSession(h.sessionID)
    if (h.anchorSeq > 0) {
      await store.jumpToSeq(h.anchorSeq)
    }
    emit('close')
  } catch (e) {
    toast('error', errText(e))
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter') {
    e.preventDefault()
    void run()
  }
}

onMounted(() => void nextTickTick())
async function nextTickTick() {
  await Promise.resolve()
  inputEl.value?.focus()
}
</script>

<template>
  <BaseModal open title="搜索所有会话" @close="emit('close')">
    <p class="px-1 pb-2 text-xs text-[var(--c-text-dim)]">
      在所有会话里搜（Ctrl+Shift+F）。回车搜索；点一条结果跳到那场对话的对应轮次。
    </p>

    <div class="flex items-center gap-2">
      <input
        ref="inputEl"
        v-model="query"
        type="search"
        placeholder="搜索词（中文直接子串匹配）"
        class="min-w-0 flex-1 rounded-lg border border-[var(--c-border)] bg-[var(--c-surface)] px-2.5 py-1.5 text-xs outline-none focus:border-[var(--c-primary)]"
        aria-label="跨会话搜索词"
        @keydown="onKeydown"
      />
      <button class="btn-primary px-3 py-1.5 text-xs" :disabled="!query.trim() || loading" @click="run">
        {{ loading ? '搜索中…' : '搜索' }}
      </button>
    </div>
    <label class="mt-1.5 flex items-center gap-1.5 px-1 text-[11px] text-[var(--c-text-dim)]">
      <input v-model="scopeWs" type="checkbox" />
      只搜当前工作区<span v-if="ws.path" class="text-[var(--c-text-faint)]">（{{ ws.path }}）</span>
    </label>

    <p v-if="error" class="mt-2 px-1 text-xs text-[var(--c-err-text)]" role="alert">{{ error }}</p>

    <div class="mt-2 max-h-[52vh] min-h-[6rem] overflow-y-auto">
      <p v-if="!searched && !loading" class="px-1 py-6 text-center text-xs text-[var(--c-text-faint)]">
        输入搜索词，回车开始。
      </p>
      <p v-else-if="!loading && !hits.length" class="px-4 py-6 text-center text-xs text-[var(--c-text-faint)]">
        <template v-if="truncatedLedgers">
          没有搜到。但有 {{ truncatedLedgers }} 本长会话只扫了开头 {{ scanLimitMB }}MB，<br />
          它们后面的内容没进这次搜索——换个关键词，或把范围限定到当前工作区再试。
        </template>
        <template v-else>没有会话里包含这个搜索词。</template>
      </p>
      <template v-else>
        <p
          v-if="truncatedLedgers"
          class="mb-1 px-1 text-[10px] text-[var(--c-text-faint)]"
          data-test="scan-truncated-note"
        >
          有 {{ truncatedLedgers }} 本长会话只扫了开头 {{ scanLimitMB }}MB，这部分结果可能不全。
        </p>
        <ul class="space-y-1">
        <li v-for="(h, i) in hits" :key="`${h.sessionID}-${h.anchorSeq}-${i}`">
          <button
            class="w-full rounded-lg border border-[var(--c-border)] bg-[var(--c-surface)] px-2.5 py-1.5 text-left transition-colors hover:border-[var(--c-primary)]"
            @click="openHit(h)"
          >
            <div class="flex items-baseline gap-2">
              <span class="min-w-0 flex-1 truncate text-xs font-medium text-[var(--c-text)]" :title="h.sessionTitle">
                {{ h.sessionTitle || h.sessionID }}
              </span>
              <span
                class="shrink-0 text-[10px]"
                :class="h.role === 'user' ? 'text-[var(--c-primary)]' : 'text-[var(--c-text-faint)]'"
              >
                {{ h.role === 'user' ? '我' : 'AI' }}
              </span>
            </div>
            <p class="mt-0.5 line-clamp-2 text-[11px] leading-4 text-[var(--c-text-dim)]">{{ h.snippet }}</p>
            <p class="mt-0.5 flex items-center gap-2 text-[10px] text-[var(--c-text-faint)]">
              <span v-if="h.workspace" class="min-w-0 truncate">{{ h.workspace }}</span>
              <span v-if="h.truncated" title="该账本超过扫描字节上限，命中可能不全">（扫描已截断）</span>
            </p>
          </button>
        </li>
      </ul>
      </template>
    </div>
  </BaseModal>
</template>
