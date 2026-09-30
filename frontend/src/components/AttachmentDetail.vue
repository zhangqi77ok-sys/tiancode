<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { registerEsc } from '../composables/useEsc'
import AppIcon from './AppIcon.vue'

// 附件本机详情（0.0.25）：点附件名字打开。只显示**消息上已有**的字段——
// 文件名、类型（图片/文件 + mediaType）、内联方式、本机路径；图片用现成的 dataUrl
// 显示原图，普通文件不预览二进制（不假装读了内容）。
// 不跳转、不使用公网链接（本地开源工具，图片始终是请求体内的内联数据）。
// Esc 或点外面关闭：经 Esc 消费栈注册，关浮层不会打断正在跑的回合。
type Att = {
  kind: string
  name: string
  mediaType?: string
  dataUrl?: string
  path?: string
  inline?: string
}
const props = defineProps<{ att: Att }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const isImage = computed(() => props.att.kind === 'image')
const kindText = computed(
  () => `${isImage.value ? '图片' : '文件'}${props.att.mediaType ? ` · ${props.att.mediaType}` : ''}`,
)

// 内联方式：与发送时一致的三态（full / path / none）；没记录就照实说"未记录"
const inlineText = computed(() => {
  switch (props.att.inline) {
    case 'full':
      return '已内联（内容随请求一起发给模型）'
    case 'path':
      return '只附路径（模型需要时自己读）'
    case 'none':
      return '未内联（模型没收到内容）'
    default:
      return '未记录'
  }
})

// 还在不在本机：图片有 dataUrl = 发送时或重放时读到了附件文件；没有 dataUrl 说明读不到
//（被移走或删掉）。普通文件没有这条信号——前端不能 stat 本机磁盘，只列路径、不猜。
const missing = computed(() => isImage.value && !props.att.dataUrl)

const root = ref<HTMLElement | null>(null)

// Esc：面板挂载 = 打开（由父组件的 v-if 控制），mounted 注册即"打开时注册"
let offEsc: (() => void) | null = null
onMounted(() => {
  offEsc = registerEsc(() => emit('close'))
})
onBeforeUnmount(() => offEsc?.())
</script>

<template>
  <Teleport to="body">
    <div
      class="fixed inset-0 z-[var(--z-modal)] grid place-items-center bg-black/30 p-6"
      @click.self="emit('close')"
    >
      <div
        ref="root"
        role="dialog"
        aria-label="附件详情"
        class="max-h-[80vh] w-full max-w-lg overflow-y-auto rounded-[var(--r-card)] border border-[var(--c-border)] bg-[var(--c-surface)] p-4 shadow-[var(--shadow-float)]"
      >
        <div class="flex items-start gap-2">
          <AppIcon
            :name="isImage ? 'image' : 'file'"
            :size="16"
            class="mt-0.5 shrink-0 text-[var(--c-text-dim)]"
          />
          <div class="min-w-0 flex-1">
            <p class="break-all text-sm font-medium">{{ att.name }}</p>
            <p class="mt-0.5 text-xs text-[var(--c-text-dim)]">{{ kindText }}</p>
          </div>
          <button class="chip" title="关闭（Esc）" @click="emit('close')">关闭</button>
        </div>

        <!-- 图片：用现成的 dataUrl 显示原图（本地内联数据，不是公网链接） -->
        <div v-if="isImage" class="mt-3">
          <img
            v-if="att.dataUrl"
            :src="att.dataUrl"
            class="max-h-[45vh] w-full rounded-[var(--r-card)] border border-[var(--c-border)] object-contain"
            :alt="att.name"
          />
          <p
            v-else
            class="rounded-[var(--r-card)] border border-dashed border-[var(--c-border)] px-3 py-2 text-xs text-[var(--c-text-dim)]"
          >
            文件已不在本机（读不到附件文件，无法显示原图）——名字仍留着，方便对账
          </p>
        </div>

        <div class="mt-3 space-y-1.5 text-xs">
          <p>
            <span class="text-[var(--c-text-faint)]">内联方式</span> · {{ inlineText }}
          </p>
          <p class="break-all">
            <span class="text-[var(--c-text-faint)]">本机路径</span> ·
            {{ att.path || '未记录' }}
          </p>
          <p v-if="!isImage" class="text-[var(--c-text-faint)]">
            普通文件不在此预览内容：要读内容可以让模型用 fs 工具，或在本机打开该路径。
          </p>
          <p v-if="missing" class="text-[var(--c-warn-text)]">
            文件已不在本机：附件目录里的文件已被移走或删除。
          </p>
        </div>
      </div>
    </div>
  </Teleport>
</template>
