<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ChatMsg, PendingAttachment } from '../stores/chat'
import { useChatStore } from '../stores/chat'
import { useEscClose } from '../composables/useEsc'
import { useToast } from '../composables/useToast'
import AppIcon from './AppIcon.vue'
import ApprovalCard from './ApprovalCard.vue'
import AttachmentDetail from './AttachmentDetail.vue'
import AskCard from './AskCard.vue'
import MarkdownBody from './MarkdownBody.vue'
import ToolCard from './ToolCard.vue'

// 两种形态：
// - 用户消息（role=user）：单气泡，右对齐；
// - agent 回合（run）：单一消息头 + 内部段落流（思考 → 文本 → 工具卡 → 思考 → …）——
//   整个回合是"一个输出"，不再每个助手段各起一个 AGENT 头（0.2.16 用户反馈）。
const props = defineProps<{ m: ChatMsg; run?: ChatMsg[] }>()

// 重跑（第 7 批）：不再由气泡直接重发——把原文回填输入框让人改，确认分叉后才发送。
// 载荷带上这条消息原有的附件（从当前前端消息上取，不动账本格式）。
const emit = defineEmits<{
  (e: 'rerun', payload: { seq: number; text: string; attachments: PendingAttachment[] }): void
}>()

const segments = computed<ChatMsg[]>(() => props.run ?? [props.m])
const isUser = computed(() => props.m.role === 'user')

// 附件本机详情（0.0.25）：点附件名字打开（文件与图片一样都点得动）。
// 只显示消息上已有的字段，不跳转、不用公网链接。
const detail = ref<NonNullable<ChatMsg['attachments']>[number] | null>(null)

// 附件的 hover 说明：内联与否决定"模型能不能看到内容"，不能只藏在版面之外
function attTitle(a: { name: string; path?: string; inline?: string }): string {
  const how =
    a.inline === 'full'
      ? '已内联（内容随请求一起发给模型）'
      : a.inline === 'none'
        ? '未内联（模型没收到内容）'
        : '只附路径（模型需要自己读）'
  return `${a.name} · ${how}${a.path ? ` · ${a.path}` : ''} · 点击看本机详情`
}
// 图片放大（0.0.10）：点缩略图全屏看原图
const lightbox = ref<string | null>(null)

// Esc 关闭灯箱（第 3 批）：经消费栈注册——"关灯箱"绝不能顺手把正在跑的回合中断
useEscClose(
  computed(() => !!lightbox.value),
  () => {
    lightbox.value = null
  },
)

const { push: toast } = useToast()
const store = useChatStore()

// 附件回填（第 7 批）：账本/重放里的附件是 {dataUrl, path} 形态，待发送区要
// {dataB64, sourcePath}——这里做一次形态转换（图片 base64 直接搬），不动账本格式。
function toPending(a: NonNullable<ChatMsg['attachments']>[number]): PendingAttachment {
  const comma = a.dataUrl ? a.dataUrl.indexOf(',') : -1
  return {
    kind: a.kind === 'image' ? 'image' : 'file',
    name: a.name,
    mediaType: a.mediaType || 'application/octet-stream',
    size: 0, // 回显不带宽高：只影响待发送区的尺寸文案，不影响发送内容
    dataB64: comma >= 0 ? a.dataUrl!.slice(comma + 1) : undefined,
    sourcePath: a.path,
    inline: a.inline === 'full' || a.inline === 'none' ? a.inline : 'path',
  }
}

// 重跑（第 7 批）：只把原文与附件交回输入框（可改）；撤回与分叉在用户按下发送、
// 确认之后才发生（见 Composer.submitRerun）——气泡这里绝不撤回任何东西。
function startRerun() {
  if (!props.m.seq || store.running) return
  emit('rerun', {
    seq: props.m.seq,
    text: props.m.content ?? '',
    attachments: (props.m.attachments ?? []).map(toPending),
  })
}

// 思考折叠：按段独立记忆；流式段默认展开，终态后回到折叠（用户手动开合后以手动为准）
const thinkingOpen = ref<Record<string, boolean>>({})
function segKey(seg: ChatMsg, i: number): string {
  return seg.id ?? `seg-${i}`
}
function isThinkingOpen(seg: ChatMsg, i: number): boolean {
  return thinkingOpen.value[segKey(seg, i)] ?? !!seg.streaming
}
function toggleThinking(seg: ChatMsg, i: number) {
  thinkingOpen.value[segKey(seg, i)] = !isThinkingOpen(seg, i)
}

// 回合头：时间取首个助手段；耗时取各段之和（通常只有终段带）
const startedAt = computed(() => segments.value.find((s) => s.role === 'assistant')?.at)
const durationMs = computed(() => segments.value.reduce((a, s) => a + (s.durationMs ?? 0), 0))

// 复制这条用户消息的正文（第 7 批）：剪贴板失败必须说清，不假装成功
async function copyUser() {
  try {
    await navigator.clipboard.writeText(props.m.content ?? '')
    toast('info', '已复制消息')
  } catch {
    toast('error', '复制失败：剪贴板不可用')
  }
}

async function copyMessage() {
  const text = segments.value
    .filter((s) => s.role === 'assistant')
    .map((s) => s.content)
    .filter(Boolean)
    .join('\n\n')
  try {
    await navigator.clipboard.writeText(text)
    toast('info', '已复制回复')
  } catch {
    toast('error', '复制失败：剪贴板不可用')
  }
}

function fmtTime(at?: number): string {
  if (!at) return ''
  return new Date(at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}
</script>

<template>
  <!-- 用户消息：主色实心气泡，右对齐；附件（0.0.10）在气泡上方——图片可点击放大 -->
  <div v-if="isUser" class="flex flex-col items-end gap-1">
    <div class="flex items-center gap-2 text-xs text-[var(--c-text-dim)]">
      <span>你</span><span>{{ fmtTime(m.at) }}</span>
      <!-- 复制（第 7 批）：复制这条消息的正文，失败 toast（不假装成功） -->
      <button
        v-if="m.content"
        class="flex items-center gap-0.5 text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-primary)]"
        title="复制这条消息"
        aria-label="复制这条消息"
        @click="copyUser"
      >
        <AppIcon name="copy" :size="11" />复制
      </button>
      <!-- 重跑（第 7 批）：仅账本已有该消息（有 seq）且空闲时可用；点了先把原文放回
           输入框（可改），撤回与分叉等用户按发送时确认 -->
      <button
        v-if="m.seq && !store.running"
        class="flex items-center gap-0.5 text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-primary)]"
        title="重跑：把这条消息放回输入框（可改），确认后撤回其后的文件改动并重新执行"
        @click="startRerun"
      >
        <AppIcon name="refresh" :size="11" />重跑
      </button>
    </div>
    <!-- 附件（0.0.25）：每个附件都是**可点的名字**（文件与图片一样），点开本机详情。
         图片有 dataUrl 时在名字旁留一枚缩略图（点缩略图仍是原来的放大预览）；
         没有 dataUrl（附件已不在本机）也保留名字——此前整张图和名字一起消失，
         用户以为消息没带附件。 -->
    <div v-if="m.attachments?.length" class="flex max-w-[85%] flex-wrap items-center justify-end gap-1.5">
      <button
        v-for="(a, i) in m.attachments"
        :key="`att-${i}`"
        class="inline-flex max-w-[16rem] items-center gap-1.5 rounded-md border border-[var(--c-border)] bg-[var(--c-primary-soft)] px-2 py-1 text-xs text-[var(--c-primary)] transition-colors hover:border-[var(--c-primary)]"
        :title="attTitle(a)"
        @click="detail = a"
      >
        <img
          v-if="a.kind === 'image' && a.dataUrl"
          :src="a.dataUrl"
          class="h-6 w-6 shrink-0 cursor-zoom-in rounded object-cover"
          :alt="a.name"
          title="点击放大预览"
          @click.stop="lightbox = a.dataUrl!"
        />
        <AppIcon v-else :name="a.kind === 'image' ? 'image' : 'file'" :size="12" class="shrink-0" />
        <span class="min-w-0 truncate">{{ a.name }}</span>
      </button>
    </div>
    <!-- 正文：主色实心气泡，右对齐 -->
    <div
      v-if="m.content"
      class="max-w-[85%] whitespace-pre-wrap rounded-2xl bg-[var(--c-primary)] px-4 py-2.5 text-sm leading-6 text-white"
    >
      {{ m.content }}
    </div>
  </div>

  <!-- agent 回合：单一消息头 + 段落流，整体是一个输出 -->
  <div v-else class="flex flex-col items-start gap-1">
    <div class="flex items-center gap-2 text-xs text-[var(--c-text-dim)]">
      <span class="font-medium">tiancode</span><span>{{ fmtTime(startedAt) }}</span>
      <span v-if="durationMs" class="text-[var(--c-text-faint)]">· {{ (durationMs / 1000).toFixed(1) }}s</span>
      <button
        class="ml-1 inline-flex h-5 w-5 items-center justify-center rounded text-[var(--c-text-faint)] opacity-60 transition-opacity hover:text-[var(--c-primary)] hover:opacity-100"
        title="复制回复"
        aria-label="复制回复"
        @click="copyMessage"
      >
        <AppIcon name="copy" :size="12" />
      </button>
    </div>

    <!-- 段落流：ReAct 顺序原样保留，不做二次归并 -->
    <template v-for="(seg, i) in segments" :key="seg.id ?? 'seg-' + i">
      <template v-if="seg.role === 'assistant'">
        <button
          v-if="seg.thinking"
          class="chip text-xs"
          :aria-expanded="isThinkingOpen(seg, i)"
          @click="toggleThinking(seg, i)"
        >
          <AppIcon
            name="chevron-down"
            :size="12"
            class="transition-transform"
            :class="isThinkingOpen(seg, i) ? '' : '-rotate-90'"
          />
          深度思考
        </button>
        <pre
          v-if="seg.thinking && isThinkingOpen(seg, i)"
          class="tool-full max-w-[85%] text-[var(--c-text-dim)]"
          >{{ seg.thinking }}</pre
        >

        <!-- "正在思考"占位（0.2.28）：首块到达前的可见反馈——慢上游/挂起时
             用户看到的是"在等模型"，而不是"什么都没发生" -->
        <div
          v-if="seg.streaming && !seg.content && !seg.thinking"
          class="flex max-w-[85%] items-center gap-1.5 rounded-2xl border border-[var(--c-border)] bg-[var(--c-bubble)] px-4 py-3"
          aria-label="正在思考"
        >
          <span class="h-1.5 w-1.5 animate-bounce rounded-full bg-[var(--c-text-faint)]"></span>
          <span class="h-1.5 w-1.5 animate-bounce rounded-full bg-[var(--c-text-faint)] [animation-delay:150ms]"></span>
          <span class="h-1.5 w-1.5 animate-bounce rounded-full bg-[var(--c-text-faint)] [animation-delay:300ms]"></span>
          <span class="ml-1 text-xs text-[var(--c-text-faint)]">正在思考…</span>
        </div>

        <!-- 纯思考段（无正文）不出空气泡 -->
        <div
          v-if="seg.content || seg.error || seg.term === 3 || seg.term === 4"
          class="max-w-[85%] rounded-2xl border px-4 py-3 text-sm leading-6"
          :class="
            seg.term === 3 || seg.term === 4
              ? 'border-[var(--c-warn)] bg-[var(--c-warn-soft)]'
              : seg.error
                ? 'border-[var(--c-err)] bg-[var(--c-err-soft)]'
                : 'border-[var(--c-border)] bg-[var(--c-bubble)]'
          "
        >
          <div v-if="seg.error || seg.term === 3 || seg.term === 4" class="flex items-start gap-2 whitespace-pre-wrap">
            <AppIcon
              name="alert"
              :size="14"
              class="mt-1 shrink-0"
              :class="seg.term === 3 || seg.term === 4 ? 'text-[var(--c-warn-text)]' : 'text-[var(--c-err-text)]'"
            />
            <span>{{ seg.content }}</span>
          </div>
          <template v-else>
            <MarkdownBody :content="seg.content" :streaming="seg.streaming" />
          </template>
        </div>
      </template>

      <!-- 空卡守卫（0.0.06）：没有标题/内容/diff 且非执行中的工具事件不出卡——
           绝不让用户看到一张"什么都没有"的空卡 -->
      <ToolCard v-else-if="seg.role === 'tool' && (seg.title || seg.content || seg.diff || seg.status === 'running')" :m="seg" />
      <!-- role === 'todo' 刻意不渲染：任务清单由悬浮件（FloatingTodo）承载，不随对话滚走 -->
      <AskCard v-else-if="seg.role === 'ask'" :m="seg" />
      <ApprovalCard v-else-if="seg.role === 'approval'" :m="seg" />
    </template>
  </div>

  <!-- 图片放大遮罩（0.0.10）：点缩略图全屏看原图 -->
  <Teleport to="body">
    <div
      v-if="lightbox"
      class="fixed inset-0 z-[var(--z-modal)] grid place-items-center bg-black/80 p-8"
      @click="lightbox = null"
    >
      <img :src="lightbox" class="max-h-full max-w-full rounded-lg" alt="放大图片" />
    </div>
  </Teleport>

  <!-- 附件本机详情（0.0.25）：Esc / 点外面关闭，走现有浮层栈——不打断正在跑的回合 -->
  <AttachmentDetail v-if="detail" :att="detail" @close="detail = null" />
</template>
