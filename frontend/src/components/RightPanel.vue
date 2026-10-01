<script lang="ts">
// 右栏 tab 注册表（容器与 App.vue 的扩展契约，见下方 setup 注释）
import type { IconName } from './AppIcon.vue'

export interface RightPanelTabDef {
  id: string // tab 唯一标识，内容插槽名即 `#tab-${id}`
  label: string // tab 条文字
  icon: IconName // tab 条图标
  active: boolean // 是否当前激活（真相在 store.rightPanelTab，App.vue 组装时标注）
  activate: () => void // 点击 tab：App.vue 据此置 store 状态（含内容侧的打开动作）
  close: () => void // Esc 消费：关这个 tab（内容侧各自收敛自己的开态）
  badge?: number // 可选角标（未来「任务」tab 可显示待办数）
}
</script>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { registerEsc } from '../composables/useEsc'
import AppIcon from './AppIcon.vue'

// 右栏容器（0.0.28）：aside + tab 条 + Esc 消费栈，对 tab 内容零知识。
// tab 扩展契约（新增「目录」「任务」等照此办理，容器零改动）：
//   1. App.vue 组装 RightPanelTabDef[] 传入——isOpen 的 tab 才进注册表（即 tab 条），
//      全部关闭时 App.vue 不挂载本容器（v-if）；
//   2. 内容经同名插槽 `#tab-${id}` 提供，激活哪个渲染哪个；
//   3. active/activate/close 都收敛到 store：active = store.rightPanelTab === id，
//      activate 置激活态（含内容侧打开动作），close 关内容侧开态——关掉激活 tab 后
//      注册表自然缩回，激活态不在列表时容器回退到第一项。
const props = defineProps<{ tabs: RightPanelTabDef[] }>()

// 激活 tab：store 标注优先，缺失（激活 tab 已被关掉）回退第一项
const active = computed(() => props.tabs.find((t) => t.active) ?? props.tabs[0])

// Esc：容器挂载 = 面板打开（App.vue v-if 控制），注册即"打开时入栈"。
// 按当前激活 tab 关闭；内容侧开态清零后注册表缩回，若还有 open 的 tab 则面板
// 留着并回退显示它，否则整个面板退场（v-if 卸载，这里自动注销）。
let offEsc: (() => void) | null = null
onMounted(() => {
  offEsc = registerEsc(() => active.value?.close())
})
onBeforeUnmount(() => offEsc?.())
</script>

<template>
  <aside
    class="flex w-[420px] shrink-0 flex-col border-l border-[var(--c-border)] xl:w-[480px]"
    :aria-label="active ? `${active.label}面板` : '右栏面板'"
  >
    <!-- tab 条：chip + aria-pressed 是全库统一的"选中态真相"（见 style.css） -->
    <div class="flex items-center gap-1.5 border-b border-[var(--c-border)] px-2.5 py-2" role="group" aria-label="右栏视图">
      <button
        v-for="t in tabs"
        :key="t.id"
        class="chip px-3 py-1 text-xs"
        :aria-pressed="t.id === active?.id"
        :title="t.label"
        @click="t.activate()"
      >
        <AppIcon :name="t.icon" :size="12" />
        {{ t.label }}
        <span
          v-if="t.badge"
          class="ml-0.5 rounded-full bg-[var(--c-primary-soft)] px-1.5 text-[10px] tabular-nums text-[var(--c-primary)]"
        >{{ t.badge }}</span>
      </button>
    </div>
    <!-- 激活内容：动态插槽（tabs 空时 App 层本不该挂载，这里兜底渲染空） -->
    <div class="flex min-h-0 flex-1 flex-col">
      <slot v-if="active" :name="`tab-${active.id}`" />
    </div>
  </aside>
</template>
