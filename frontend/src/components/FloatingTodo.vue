<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { TodoItem } from '../stores/chat'
import { useChatStore } from '../stores/chat'
import {
  DEFAULT_POS,
  clampPos,
  isDrag,
  latestTodos,
  loadCollapsed,
  loadHidden,
  loadPos,
  saveCollapsed,
  saveHidden,
  savePos,
  type FloatPos,
} from '../composables/floatingTodo'
import AppIcon from './AppIcon.vue'

// 悬浮任务清单（0.2.24 用户反馈）：任务清单不再内联在消息流里随对话滚走，
// 而是浮在对话面板上层的一块面板；点击在"悬浮列表 ⇄ 悬浮图标"两态间切换，
// 两态都可拖动，位置/折叠/隐藏态跨重启保留。
//
// 手势实现要点（0.2.26 修复）：pointerdown/move/up 必须绑在 root（
// setPointerCapture 的捕获目标）——捕获后事件只派发到捕获元素及其祖先，
// 挂在子按钮上永远收不到（实机：拖不动、点不开）。同时**越过拖动阈值才捕获**：
// 点击路径保持原生 click 语义（含键盘 Enter/Space 触发 toggle）。
const store = useChatStore()

const todos = computed(() => latestTodos(store.messages))
const items = computed<TodoItem[]>(() => todos.value?.items ?? [])
const done = computed(() => items.value.filter((t) => t.status === 'done').length)
const total = computed(() => items.value.length)

const collapsed = ref(loadCollapsed())
const hidden = ref(loadHidden())
const pos = ref<FloatPos | null>(loadPos())
const root = ref<HTMLElement | null>(null)

// 渲染判据：无任务清单 / 清单纯空白（0/0 空面板无意义）/ 用户已隐藏 → 不渲染
const visible = computed(() => !!todos.value && items.value.length > 0 && !hidden.value)

const style = computed(() => {
  const p = pos.value ?? DEFAULT_POS
  return { left: `${p.x}px`, top: `${p.y}px` }
})

// 新的一份任务清单出现（id 变化）即展开并取消隐藏：这是"本轮计划"最该被看见的时刻。
// 只改内存不改持久化（除取消隐藏外）——用户手动折叠过的偏好留给下次启动生效。
const activeId = computed(() => todos.value?.id ?? '')
watch(activeId, (id) => {
  if (!id) return
  collapsed.value = false
  if (hidden.value) {
    hidden.value = false
    saveHidden(false)
  }
})

function hide() {
  hidden.value = true
  saveHidden(true)
}

// bounds 排除输入区（Composer）：悬浮件盖住输入框/发送键是明确的坏体验
function bounds(): { w: number; h: number } {
  const parent = root.value?.parentElement
  if (!parent) return { w: 0, h: 0 }
  const composer = parent.querySelector('[data-composer]')
  const reserve = composer instanceof HTMLElement ? composer.offsetHeight + 12 : 96
  return { w: parent.clientWidth, h: Math.max(0, parent.clientHeight - reserve) }
}

function selfSize(): { w: number; h: number } {
  const el = root.value
  return { w: el?.offsetWidth ?? 0, h: el?.offsetHeight ?? 0 }
}

// —— 拖动：阈值内算点击（不拖也能点开），超出阈值才算拖动并落盘 ——
let startX = 0
let startY = 0
let originX = 0
let originY = 0
let dragging = false
let moved = false
let pendingId = -1

function onPointerDown(e: PointerEvent) {
  if (e.button !== 0) return
  const el = root.value
  if (!el) return
  const target = e.target as HTMLElement | null
  // 只有把手（标题栏/收起图标）上的按下参与手势；隐藏按钮标了 data-no-drag。
  // 面板主体（任务列表）内的按下不启动拖拽，避免拖列表时误移动面板。
  if (!target?.closest('[data-drag-handle]') || target.closest('[data-no-drag]')) return
  const base = pos.value ?? DEFAULT_POS
  startX = e.clientX
  startY = e.clientY
  originX = base.x
  originY = base.y
  dragging = true
  moved = false
  pendingId = e.pointerId
}

function onPointerMove(e: PointerEvent) {
  if (!dragging) return
  const dx = e.clientX - startX
  const dy = e.clientY - startY
  if (!moved && isDrag(dx, dy)) {
    moved = true
    // 越过阈值才捕获指针：点击路径不捕获，原生 click（与键盘触发）保持可用
    root.value?.setPointerCapture(pendingId)
  }
  if (!moved) return
  pos.value = clampPos({ x: originX + dx, y: originY + dy }, selfSize(), bounds())
}

function onPointerUp(e: PointerEvent) {
  if (!dragging) return
  dragging = false
  pendingId = -1
  const el = root.value
  if (el?.hasPointerCapture(e.pointerId)) el.releasePointerCapture(e.pointerId)
  if (moved && pos.value) savePos(pos.value)
}

// 点击切换两态（把手按钮的原生 click；键盘 Enter/Space 同样走这里）。
// 拖动结束后 pointerup 被重定向到 root，按钮不会收到 click——无需额外忽略逻辑。
function toggle() {
  collapsed.value = !collapsed.value
  saveCollapsed(collapsed.value)
}

// 面板尺寸变化（窗口缩放、侧栏开合）后把面板收回可视区，避免贴边后被裁掉；
// 钳制结果同步落盘，否则下次启动又回到视野外
function reflow() {
  if (!pos.value) return
  const clamped = clampPos(pos.value, selfSize(), bounds())
  if (clamped.x !== pos.value.x || clamped.y !== pos.value.y) {
    pos.value = clamped
    savePos(clamped)
  }
}

onMounted(() => {
  // 先钳制再挂监听：换过更小窗口/布局后，旧坐标可能落在可视区外（找不回来）
  if (pos.value) {
    const clamped = clampPos(pos.value, selfSize(), bounds())
    pos.value = clamped
    savePos(clamped)
  }
  window.addEventListener('resize', reflow)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', reflow)
})

function statusClass(t: TodoItem): string {
  if (t.status === 'done') return 'border-[var(--c-ok)] bg-[var(--c-ok)] text-white'
  if (t.status === 'in_progress') return 'border-[var(--c-primary)]'
  return 'border-[var(--c-border)]'
}

// 手势统一绑在 root（捕获目标）：见文件头注释
const dragHandlers = {
  onPointerdown: onPointerDown,
  onPointermove: onPointerMove,
  onPointerup: onPointerUp,
  onPointercancel: onPointerUp,
}
</script>

<template>
  <div v-if="visible" ref="root" class="absolute z-20" :style="style" v-bind="dragHandlers">
    <!-- 展开态：悬浮列表 -->
    <div
      v-if="!collapsed"
      class="flex w-[280px] flex-col overflow-hidden rounded-xl border border-[var(--c-border)] bg-[var(--c-surface-soft)] shadow-[var(--shadow-float)]"
      role="region"
      aria-label="任务清单"
    >
      <div
        data-drag-handle
        class="flex cursor-grab touch-none items-center gap-2 px-3 py-2 text-xs select-none active:cursor-grabbing"
        title="按住可拖动"
      >
        <button
          class="flex min-w-0 flex-1 items-center gap-2 text-left"
          aria-expanded="true"
          title="点击收起为悬浮图标"
          @click="toggle"
        >
          <AppIcon name="check" :size="13" class="shrink-0 text-[var(--c-primary)]" />
          <span class="shrink-0 font-medium text-[var(--c-text)]">任务清单</span>
          <span class="text-[var(--c-text-faint)]">{{ done }}/{{ total }} 已完成</span>
          <AppIcon name="minus" :size="12" class="ml-auto shrink-0 text-[var(--c-text-faint)]" />
        </button>
        <button
          data-no-drag
          class="shrink-0 rounded p-0.5 text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-text)]"
          aria-label="隐藏任务清单"
          title="隐藏（新任务清单出现时自动恢复）"
          @click="hide"
        >
          <AppIcon name="x" :size="12" />
        </button>
      </div>
      <div class="max-h-[45vh] space-y-1 overflow-y-auto px-3 pb-2.5">
        <div v-for="(t, i) in items" :key="`${t.text}-${i}`" class="flex items-start gap-2 text-xs leading-5">
          <span
            class="mt-0.5 flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-full border"
            :class="statusClass(t)"
          >
            <AppIcon v-if="t.status === 'done'" name="check" :size="9" />
          </span>
          <span
            :class="
              t.status === 'done'
                ? 'text-[var(--c-text-faint)] line-through'
                : t.status === 'in_progress'
                  ? 'font-medium text-[var(--c-text)]'
                  : 'text-[var(--c-text-dim)]'
            "
          >
            {{ t.text }}
          </span>
        </div>
      </div>
    </div>

    <!-- 收起态：悬浮图标（圆形 + 角标进度，点一下即展开；进度不因收起而丢失） -->
    <button
      v-else
      data-drag-handle
      class="relative flex h-10 w-10 cursor-grab touch-none items-center justify-center rounded-full border border-[var(--c-border)] bg-[var(--c-surface-soft)] shadow-[var(--shadow-float)] select-none active:cursor-grabbing"
      :title="`任务清单 ${done}/${total} 已完成 · 点击展开 · 按住可拖动`"
      aria-label="展开任务清单"
      aria-expanded="false"
      @click="toggle"
    >
      <AppIcon name="check" :size="15" class="text-[var(--c-primary)]" />
      <span
        class="absolute -top-1.5 -right-2 rounded-full bg-[var(--c-primary)] px-1.5 py-px text-[10px] leading-4 font-medium text-white ring-2 ring-[var(--c-surface)]"
      >
        {{ done }}/{{ total }}
      </span>
    </button>
  </div>
</template>
