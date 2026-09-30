<script setup lang="ts">
import { computed, inject, onBeforeUnmount, onMounted, ref } from 'vue'
import { useChannelStore } from '../stores/channels'
import { useChatStore, type PendingAttachment } from '../stores/chat'
import { parseAtToken, applyPick, type AtToken } from '../composables/atFile'
import { useEscClose } from '../composables/useEsc'
import { bridge } from '../wails'
import AppIcon from './AppIcon.vue'

// 输入框高度（0.0.06）：默认约 3 行（72px）；可拖到大约半屏（50vh）。
// 自动增高与手动拖拽并存：autoGrow 封顶 50vh，用户拖过之后以用户为准。
const MIN_INPUT_HEIGHT_PX = 72
const MAX_INPUT_HEIGHT = '50vh'

const store = useChatStore()
const channels = useChannelStore()
const draft = defineModel<string>({ required: true })
const box = ref<HTMLTextAreaElement | null>(null)

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

function onCaretChange() {
  const t = box.value
  if (!t) return
  const token = parseAtToken(draft.value, t.selectionStart ?? 0)
  atFile.value = token
  if (!token) {
    atHits.value = []
    atNotice.value = ''
    return
  }
  if (atTimer) clearTimeout(atTimer)
  atTimer = setTimeout(async () => {
    try {
      atHits.value = (await bridge().app.SearchWorkspaceFiles(store.sessionId, token.query)) ?? []
      atIndex.value = 0
      atNotice.value = atHits.value.length ? '' : '没有匹配的文件'
    } catch (e) {
      // 纯对话没有工作区：提示而非报错，也不去用顶栏里下一场新对话的根
      atHits.value = []
      atNotice.value = String(e instanceof Error ? e.message : e)
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

// ---- 附件（0.0.10）：粘贴/拖放/上传三路进入同一个待发送区 ----
const atts = ref<PendingAttachment[]>([])
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
const modelMenuOpen = ref(false)
const modelMenuRef = ref<HTMLElement | null>(null)
const modelOptions = computed(() => channels.modelOptions)

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

onMounted(() => document.addEventListener('mousedown', onDocMousedown))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocMousedown))

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
  if (store.running) {
    // 回合进行中：入队（终态后自动依次发出）；附件随文字一起入队并清空待发送区——
    // 只排文字会让续发的那条丢掉截图/文件（0.0.11）
    store.enqueue(text, curAtts)
    draft.value = ''
    atts.value = []
    resetBox()
    return
  }
  draft.value = ''
  atts.value = []
  resetBox()
  try {
    await store.send(text, curAtts, { throwOnError: true })
  } catch {
    // 发送失败：文字与附件全部保留在输入区，允许重试
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
</script>

<template>
  <div data-composer class="border-t border-[var(--c-border)] p-4">
    <!-- 模型选择器（0.2.28）：列出全部可用渠道的模型，点击切换；当前项标记"当前" -->
    <div ref="modelMenuRef" class="relative mb-2">
      <!-- 模型选择器行（0.0.06）：模型名不再是 11px 浅色次要字——正文级可读 -->
      <button
        class="flex items-center gap-1.5 px-1 text-xs text-[var(--c-text-dim)] transition-colors hover:text-[var(--c-text)]"
        aria-haspopup="menu"
        :aria-expanded="modelMenuOpen"
        :title="modelOptions.length ? '点击切换模型（来自已配置的渠道）' : '尚未配置模型：请在侧栏底部打开「渠道管理」'"
        @click="modelMenuOpen = !modelMenuOpen"
      >
        {{ channels.activeModel ? `模型 ${channels.activeModel}` : '未选择模型' }}
        <template v-if="channels.activeChannel"> · {{ channels.activeChannel.name }}</template>
        <AppIcon name="chevron-down" :size="11" />
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
    <!-- 输入队列：进行中提交的待发消息；立即发送 = 置顶，本轮结束最先发出。
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
        <button
          class="shrink-0 rounded p-1 text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-primary)]"
          title="立即发送（本轮结束后最先发出）"
          aria-label="置顶该消息"
          @click="store.promoteQueued(q.id)"
        >
          <AppIcon name="send" :size="12" />
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
          <span class="absolute bottom-0 left-0 right-0 rounded-b-lg bg-black/60 px-1 text-center text-[9px] text-white">
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

    <div class="relative flex items-end gap-3" :class="dragOver ? 'rounded-[var(--r-input)] ring-2 ring-[var(--c-primary)]' : ''"
      @dragover.prevent="dragOver = true"
      @dragleave.prevent="dragOver = false"
      @drop="onDrop"
    >
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
      <textarea
        :ref="setBoxRef"
        v-model="draft"
        rows="3"
        aria-label="消息输入框"
        class="min-w-0 flex-1 resize-y rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-4 py-2.5 text-sm leading-6 transition-colors focus:border-[var(--c-primary)]"
        :style="{ minHeight: MIN_INPUT_HEIGHT_PX + 'px', maxHeight: MAX_INPUT_HEIGHT }"
        placeholder="输入消息…（Enter 发送；@ 引用文件；可粘贴/拖入图片和文件）"
        @keydown="onComposerKeydown"
        @input="autoGrow"
        @click="onCaretChange"
        @keyup="onCaretChange"
        @paste="onPaste"
      ></textarea>
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
  </div>
</template>
