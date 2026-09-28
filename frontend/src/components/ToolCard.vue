<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ChatMsg } from '../stores/chat'
import AppIcon from './AppIcon.vue'

// 工具卡 = 全宽动作行（对齐商用编码智能体的会话流）：
// [状态点] [类型图标] [主标签] [动作徽章] [+N -M] …………… [查看变更/详情 ▾]
// 主标签来自内核 Title（文件名/命令首段/搜索词——最清楚自己干了谁的一方生产语义，
// ADR-0006 同纪律：结构化字段优于 UI 解析文本）；旧账本无 Title → 回退工具名 + 内容片段。
const props = defineProps<{ m: ChatMsg }>()

// 折叠态是组件本地状态：key 稳定（消息 id）后，流式更新/新工具卡插入都不再误伤它
const open = ref(false)

// 主标签：内核 Title 优先；旧数据回退"工具名 · 内容片段"
const label = computed(() => {
  const t = props.m.title?.trim()
  if (t) return t
  const name = props.m.toolName || '工具'
  const s = (props.m.content || '').replace(/\s+/g, ' ').trim()
  return s ? `${name} · ${s}`.slice(0, 80) : name
})

// 动作徽章：内核 Op → 中文
const OP_LABEL: Record<string, string> = {
  read: '读取',
  list: '浏览',
  write: '写入',
  edit: '修改',
  exec: '执行',
  search: '搜索',
  git: '版本',
}
const opLabel = computed(() => (props.m.op ? OP_LABEL[props.m.op] : ''))

// 图标按动作/工具选型：命令 → 终端；搜索 → 放大镜；写改 → 文件；git → 分支环；其余 → 扳手
const iconName = computed(() => {
  if (props.m.op === 'exec' || props.m.toolName === 'shell') return 'terminal'
  if (props.m.op === 'search' || props.m.toolName === 'search') return 'search'
  if (props.m.op === 'write' || props.m.op === 'edit' || props.m.diff) return 'file'
  if (props.m.op === 'git' || props.m.toolName === 'git') return 'refresh'
  return 'wrench'
})

// diffstat：内核 diff 统计增删行（+++ / --- 文件头不计）
const stat = computed(() => {
  if (!props.m.diff) return null
  let add = 0
  let del = 0
  for (const line of props.m.diff.split('\n')) {
    if (line.startsWith('+++') || line.startsWith('---')) continue
    if (line.startsWith('+')) add++
    else if (line.startsWith('-')) del++
  }
  return { add, del }
})

// diff 行着色：只按前缀判定（diff 由内核生成，格式稳定）；
// + / - 行作文字用 -text 色（AA 达标），装饰色只给圆点
function diffLineClass(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'text-[var(--c-text-dim)]'
  if (line.startsWith('@@')) return 'text-[var(--c-primary)]'
  if (line.startsWith('+')) return 'text-[var(--c-ok-text)]'
  if (line.startsWith('-')) return 'text-[var(--c-err-text)]'
  return 'text-[var(--c-text-dim)]'
}
</script>

<template>
  <div class="flex w-full max-w-[94%] flex-col gap-1">
    <button
      class="flex w-full items-center gap-2 rounded-xl border px-3 py-2 text-xs transition-colors"
      :class="
        m.status === 'error'
          ? 'border-[var(--c-err)] bg-[var(--c-err-soft)]'
          : 'border-[var(--c-border)] bg-[var(--c-surface-soft)] hover:border-[var(--c-primary)]'
      "
      :aria-expanded="open"
      @click="open = !open"
    >
      <span
        class="h-1.5 w-1.5 shrink-0 rounded-full"
        :class="m.status === 'error' ? 'bg-[var(--c-err)]' : 'bg-[var(--c-ok)]'"
      ></span>
      <AppIcon :name="iconName" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
      <span class="min-w-0 truncate font-medium text-[var(--c-text)]" :title="m.content">{{ label }}</span>
      <span
        v-if="opLabel"
        class="shrink-0 rounded-md bg-[var(--c-primary-soft)] px-1.5 py-0.5 text-[10px] text-[var(--c-primary)]"
      >
        {{ opLabel }}
      </span>
      <span v-if="stat" class="shrink-0 font-mono text-[11px]">
        <span class="text-[var(--c-ok-text)]">+{{ stat.add }}</span>
        <span class="ml-1 text-[var(--c-err-text)]">-{{ stat.del }}</span>
      </span>
      <span class="ml-auto shrink-0 pl-2 text-[var(--c-text-faint)]">
        {{ m.diff ? '查看变更' : '详情' }}
      </span>
      <AppIcon
        name="chevron-down"
        :size="12"
        class="shrink-0 text-[var(--c-text-faint)] transition-transform"
        :class="open ? '' : '-rotate-90'"
      />
    </button>

    <!-- 有 diff：变更面板即展开主体；无 diff：展开全文（命令卡看完整输出，只读信息不丢） -->
    <div
      v-if="open && m.diff"
      class="max-h-72 max-w-[92%] overflow-auto whitespace-pre rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] px-3 py-2 font-mono text-xs leading-5"
    >
      <div v-for="(l, li) in m.diff.split('\n')" :key="li" :class="diffLineClass(l)">{{ l }}</div>
    </div>
    <pre v-else-if="open" class="tool-full">{{ m.content }}</pre>
  </div>
</template>
