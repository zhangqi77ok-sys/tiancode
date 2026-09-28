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
  loadPos,
  saveCollapsed,
  savePos,
  type FloatPos,
} from '../composables/floatingTodo'
import AppIcon from './AppIcon.vue'

// 悬浮任务清单（0.2.24 用户反馈）：任务清单不再内联在消息流里随对话滚走，
// 而是浮在对话面板上层的一块面板；点击在"悬浮列表 ⇄ 悬浮图标"两态间切换，
// 两态都可拖动，位置与折叠态跨重启保留。
//
// 为什么挂在对话面板（App.vue 的 main，position:relative）内部而不是 fixed 到窗口：
// 拖动坐标以面板为参照系，"贴着对话区左上"的语义不随侧栏开合/窗口缩放跑偏。
//
// 为什么两态共用一个外层容器（而不是各自一个根节点）：同名模板 ref 在 v-if/v-else
// 切换时会被新节点挂载与旧节点卸载互相覆盖，可能留下 null，拖动会突然失效。
const store = useChatStore()

const todos = computed(() => latestTodos(store.messages))
const items = computed<TodoItem[]>(() => todos.value?.items ?? [])
const done = computed(() => items.value.filter((t) => t.status === 'done').length)
const total = computed(() => items.value.length)

const collapsed = ref(loadCollapsed())
const pos = ref<FloatPos | null>(loadPos())
const root = ref<HTMLElement | null>(null)

const style = computed(() => {
  const p = pos.value ?? DEFAULT_POS
  return { left: `${p.x}px`, top: `${p.y}px` }
})

// 新的一份任务清单出现（id 变化）即展开：这是"本轮计划"最该被看见的时刻。
// 只改内存不改持久化——用户手动折叠过的偏好留给下次启动生效，运行中不反复弹开。
const activeId = computed(() => todos.value?.id ?? '')
watch(activeId, (id) => {
  if (id) collapsed.value = false
})

function bounds(): { w: number; h: number } {
  const parent = root.value?.parentElement
  return { w: parent?.clientWidth ?? 0, h: parent?.clientHeight ?? 0 }
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

function onPointerDown(e: PointerEvent) {
  if (e.button !== 0) return
  const el = root.value
  if (!el) return
  const base = pos.value ?? DEFAULT_POS
  startX = e.clientX
  startY = e.clientY
  originX = base.x
  originY = base.y
  dragging = true
  moved = false
  el.setPointerCapture(e.pointerId)
}

function onPointerMove(e: PointerEvent) {
  if (!dragging) return
  const dx = e.clientX - startX
  const dy = e.clientY - startY
  if (!moved && isDrag(dx, dy)) moved = true
  if (!moved) return
  pos.value = clampPos({ x: originX + dx, y: originY + dy }, selfSize(), bounds())
}

function onPointerUp(e: PointerEvent) {
  if (!dragging) return
  dragging = false
  const el = root.value
  if (el?.hasPointerCapture(e.pointerId)) el.releasePointerCapture(e.pointerId)
  if (moved && pos.value) savePos(pos.value)
}

// 点击切换两态；刚拖过的 pointerup 会紧接着触发 click，必须忽略，
// 否则"拖完自动收起/展开"（用户只是想挪个位置）。moved 要到下次 pointerdown 才复位。
function toggle() {
  if (moved) return
  collapsed.value = !collapsed.value
  saveCollapsed(collapsed.value)
}

// 面板尺寸变化（窗口缩放、侧栏开合）后把面板收回可视区，避免贴边后被裁掉
function reflow() {
  if (pos.value) pos.value = clampPos(pos.value, selfSize(), bounds())
}
onMounted(() => window.addEventListener('resize', reflow))
onBeforeUnmount(() => {
  window.removeEventListener('resize', reflow)
})

function statusClass(t: TodoItem): string {
  if (t.status === 'done') return 'border-[var(--c-ok)] bg-[var(--c-ok)] text-white'
  if (t.status === 'in_progress') return 'border-[var(--c-primary)]'
  return 'border-[var(--c-border)]'
}

const dragHandlers = {
  onPointerdown: onPointerDown,
  onPointermove: onPointerMove,
  onPointerup: onPointerUp,
  onPointercancel: onPointerUp,
}
</script>

<template>
  <div v-if="todos" ref="root" class="absolute z-20" :style="style">
    <!-- 展开态：悬浮列表 -->
    <div
      v-if="!collapsed"
      class="flex w-[280px] flex-col overflow-hidden rounded-xl border border-[var(--c-border)] bg-[var(--c-surface-soft)] shadow-[var(--shadow-float)]"
      role="region"
      aria-label="任务清单"
    >
      <button
        v-bind="dragHandlers"
        class="flex cursor-grab touch-none items-center gap-2 px-3 py-2 text-xs select-none active:cursor-grabbing"
        title="点击收起为悬浮图标 · 按住可拖动"
        aria-expanded="true"
        @click="toggle"
      >
        <AppIcon name="check" :size="13" class="shrink-0 text-[var(--c-primary)]" />
        <span class="font-medium text-[var(--c-text)]">任务清单</span>
        <span class="text-[var(--c-text-faint)]">{{ done }}/{{ total }} 已完成</span>
        <AppIcon name="minus" :size="12" class="ml-auto shrink-0 text-[var(--c-text-faint)]" />
      </button>
      <div class="max-h-[45vh] space-y-1 overflow-y-auto px-3 pb-2.5">
        <div v-for="(t, i) in items" :key="i" class="flex items-start gap-2 text-xs leading-5">
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
      v-bind="dragHandlers"
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
