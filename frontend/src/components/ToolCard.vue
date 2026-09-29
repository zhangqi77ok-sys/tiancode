<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ChatMsg } from '../stores/chat'
import { useChatStore } from '../stores/chat'
import { useToast } from '../composables/useToast'
import { bridge } from '../wails'
import AppIcon from './AppIcon.vue'

// 工具卡 = 全宽动作行（对齐商用编码智能体的会话流）：
// [状态点] [类型图标] [主标签] [动作徽章] [+N -M] …………… [查看变更/详情 ▾]
// 主标签来自内核 Title（文件名/命令首段/搜索词——最清楚自己干了谁的一方生产语义，
// ADR-0006 同纪律：结构化字段优于 UI 解析文本）；旧账本无 Title → 回退工具名 + 内容片段。
const props = defineProps<{ m: ChatMsg }>()

const { push: toast } = useToast()

// 折叠态语义（0.0.06）：
//   - 执行中（status === 'running'）：**强制展开**且不允许折叠——输出正在生长，
//     用户需要看到车正在干什么；
//   - write/edit（有 diff）：默认展开 diff——变更就是这张卡的主体，不能藏；
//   - 其余：默认折叠，用户手动开合后以手动为准。
const userToggled = ref(false)
const open = ref(false)
const isRunning = computed(() => props.m.status === 'running')
const expandable = computed(() => isRunning.value || !!(props.m.diff && (props.m.op === 'write' || props.m.op === 'edit')))
const effectiveOpen = computed(() => (isRunning.value ? true : userToggled.value ? open.value : expandable.value))
function toggle() {
  if (isRunning.value) return // 执行中不允许折叠（结束后再收）
  userToggled.value = true
  open.value = !open.value
}

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
  tree: '结构',
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

// "在资源管理器中显示"（0.0.06）：只对文件类动作出按钮——主标签即工作区相对路径。
// shell/git/search 的主标签是命令/搜索词，没有对应的文件位置。
const revealable = computed(() => ['read', 'write', 'edit', 'list', 'tree'].includes(props.m.op ?? ''))
async function reveal() {
  try {
    await bridge().app.RevealInExplorer(props.m.title?.trim() || '')
  } catch (e) {
    toast('error', String(e instanceof Error ? e.message : e))
  }
}

// "恢复写入前"（0.0.07）：write/edit 成功且带撤销快照时出现。走后端恢复
// （旧全文只在账本里，比对哈希防覆盖用户改动）；成功后 store 追加"已恢复"卡。
const store = useChatStore()
const restoring = ref(false)
async function restore() {
  if (!props.m.callId || restoring.value) return
  restoring.value = true
  await store.restoreWrite(props.m.callId, props.m.undoPath)
  restoring.value = false
}

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
      :aria-expanded="effectiveOpen"
      @click="toggle"
    >
      <span
        class="h-1.5 w-1.5 shrink-0 rounded-full"
        :class="m.status === 'error' ? 'bg-[var(--c-err)]' : isRunning ? 'animate-pulse bg-[var(--c-warn)]' : 'bg-[var(--c-ok)]'"
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
        {{ isRunning ? '执行中' : m.diff ? '查看变更' : '详情' }}
      </span>
      <AppIcon
        name="chevron-down"
        :size="12"
        class="shrink-0 text-[var(--c-text-faint)] transition-transform"
        :class="effectiveOpen ? '' : '-rotate-90'"
      />
    </button>

    <!-- 有 diff：变更面板即展开主体（write/edit 默认展开，不再用矮容器把变更藏住）；
         无 diff：展开全文（命令卡看完整输出，只读信息不丢） -->
    <div
      v-if="effectiveOpen && m.diff"
      class="max-h-[60vh] max-w-[92%] overflow-auto whitespace-pre rounded-xl border border-[var(--c-border)] bg-[var(--c-surface)] px-3 py-2 font-mono text-xs leading-5"
    >
      <div v-for="(l, li) in m.diff.split('\n')" :key="li" :class="diffLineClass(l)">{{ l }}</div>
    </div>
    <pre v-else-if="effectiveOpen" class="tool-full">{{ m.content }}</pre>

    <!-- 撤销（0.0.07）：不可恢复说明优先显示（如超大文件未保存快照）；
         有快照时提供"恢复写入前"——走后端比对，文件被改过会被拒绝并说明 -->
    <p v-if="m.undoNote" class="self-start px-1 text-[11px] text-[var(--c-text-faint)]">{{ m.undoNote }}</p>
    <button
      v-if="m.hasUndo && m.callId && effectiveOpen"
      class="chip self-start text-[11px]"
      :disabled="restoring"
      title="把文件写回这次修改之前的内容（文件后来被改过时会被拒绝）"
      @click="restore"
    >
      <AppIcon name="refresh" :size="11" /> {{ restoring ? '正在恢复…' : '恢复写入前' }}
    </button>

    <button
      v-if="revealable && effectiveOpen"
      class="chip self-start text-[11px]"
      title="打开文件所在目录并选中"
      @click="reveal"
    >
      <AppIcon name="file" :size="11" /> 在资源管理器中显示
    </button>
  </div>
</template>
