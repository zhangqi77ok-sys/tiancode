<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useChatStore } from '../stores/chat'
import { errText } from '../composables/errText'
import { bridge, type DirEntryDTO } from '../wails'
import AppIcon from './AppIcon.vue'

// 右栏「目录」tab：懒加载逐层展开的工作区文件树（根 = 这场对话的工作区）。
// 每层只在点开时经 ListWorkspaceDir 拉取一次——大仓库不整树爆炸；点文件走现有
// openFileDetail（diff 与只读正文都在那边，与树共享同一个会话根）。会话切换即
// 重载：根随会话归属变化。宽度/边框/Esc 归 RightPanel 容器，本组件只管内容。

const store = useChatStore()

// 树节点：path 为工作区相对路径（正斜杠，'' = 根），与 openFileDetail 的路径同基准
interface DirNode {
  path: string
  name: string
  isDir: boolean
  modTime: number
  expanded: boolean
  loaded: boolean
  loading: boolean
  error: string
  children: DirNode[]
}

function toNode(e: DirEntryDTO, parentPath: string): DirNode {
  return {
    path: parentPath ? `${parentPath}/${e.name}` : e.name,
    name: e.name,
    isDir: e.isDir,
    modTime: e.modTime,
    expanded: false,
    loaded: false,
    loading: false,
    error: '',
    children: [],
  }
}

const root = ref<DirNode[]>([])
const rootLoading = ref(false)
const rootError = ref('')
// 树代际：会话切换重载后，旧代际的在途请求返回时整体丢弃（不写进新树）
let gen = 0

async function loadDir(dir: DirNode, g: number) {
  if (dir.loaded || dir.loading) return
  dir.loading = true
  dir.error = ''
  try {
    const entries = (await bridge().app.ListWorkspaceDir(store.sessionId, dir.path)) ?? []
    if (g !== gen) return // 会话已切换：过期结果丢弃
    dir.children = entries.map((e) => toNode(e, dir.path))
    dir.loaded = true
  } catch (e) {
    if (g !== gen) return
    // 失败可见：行内标注原因，保留展开态，再点一次即重试（loaded 仍为 false）
    dir.error = errText(e)
  } finally {
    if (g === gen) dir.loading = false
  }
}

async function loadRoot() {
  gen++
  const g = gen
  rootLoading.value = true
  rootError.value = ''
  try {
    const entries = (await bridge().app.ListWorkspaceDir(store.sessionId, '')) ?? []
    if (g !== gen) return
    root.value = entries.map((e) => toNode(e, ''))
  } catch (e) {
    if (g !== gen) return
    rootError.value = errText(e)
    root.value = []
  } finally {
    if (g === gen) rootLoading.value = false
  }
}

function toggleDir(node: DirNode) {
  node.expanded = !node.expanded
  if (node.expanded) void loadDir(node, gen)
}

function openFile(node: DirNode) {
  store.openFileDetail(node.path) // 内部同时把激活 tab 切到「文件」
}

// 会话切换（含草稿 ↔ 会话）：根随会话归属变化，树整体重载
watch(
  () => store.sessionId,
  () => void loadRoot(),
)
onMounted(() => void loadRoot())

// 扁平化可见行（展开的目录才下钻）：v-for 渲染一条列表，避免模板递归组件
interface Row {
  node: DirNode
  depth: number
}
const rows = computed<Row[]>(() => {
  const out: Row[] = []
  const walk = (nodes: DirNode[], depth: number) => {
    for (const n of nodes) {
      out.push({ node: n, depth })
      if (n.isDir && n.expanded) walk(n.children, depth + 1)
    }
  }
  walk(root.value, 0)
  return out
})

// 修改时间只进悬浮提示（列表密度优先）；后端没给（条目 stat 失败）就不显示
function mtimeText(node: DirNode): string {
  return node.modTime ? new Date(node.modTime).toLocaleString() : ''
}
</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col" aria-label="工作区目录树">
    <!-- 头部：标题 + 重新加载 + 关闭（Esc 经容器消费栈，这里给显式按钮） -->
    <div class="flex items-center gap-2 border-b border-[var(--c-border)] px-3 py-2.5">
      <AppIcon name="folder" :size="14" class="shrink-0 text-[var(--c-text-faint)]" />
      <div class="min-w-0 flex-1 truncate text-sm font-medium">目录</div>
      <button class="btn-icon" title="重新加载目录树" @click="loadRoot">
        <AppIcon name="refresh" :size="14" />
      </button>
      <button class="btn-icon" title="关闭（Esc）" @click="store.closeTreePanel()">
        <AppIcon name="x" :size="14" />
      </button>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto px-2 py-2">
      <!-- 顶层错误（无工作区 / 根读取失败）：可见 + 重试，绝不用空白冒充空目录 -->
      <div
        v-if="rootError"
        class="flex flex-col items-start gap-1.5 rounded-lg border border-[var(--c-err)] bg-[var(--c-err-soft)] px-2.5 py-2"
        role="alert"
      >
        <span class="flex items-center gap-1.5 text-xs text-[var(--c-err-text)]">
          <AppIcon name="alert" :size="13" />
          {{ rootError }}
        </span>
        <button class="chip text-[11px]" title="重新加载目录树" @click="loadRoot">重试</button>
      </div>
      <div v-else-if="rootLoading" class="px-2 py-6 text-center text-xs text-[var(--c-text-faint)]">读取中…</div>
      <div v-else-if="!root.length" class="px-2 py-6 text-center text-xs text-[var(--c-text-faint)]">
        工作区目录是空的
      </div>

      <ul v-else class="space-y-0.5" role="tree" aria-label="目录树">
        <li
          v-for="row in rows"
          :key="row.node.path"
          role="treeitem"
          :aria-expanded="row.node.isDir ? row.node.expanded : undefined"
        >
          <!-- 空目录显式标注（区别于"还没点开"）：与根层空态同一句文案 -->
          <div
            v-if="row.node.isDir && row.node.expanded && row.node.loaded && !row.node.children.length"
            class="py-1 text-[11px] text-[var(--c-text-faint)]"
            :style="{ paddingLeft: `${8 + (row.depth + 1) * 14}px` }"
          >
            空目录
          </div>
          <button
            v-if="row.node.isDir"
            class="flex w-full items-center gap-1.5 rounded-md py-1 pr-1.5 text-left text-xs text-[var(--c-text)] hover:bg-[var(--c-surface-soft)]"
            :style="{ paddingLeft: `${8 + row.depth * 14}px` }"
            :title="row.node.error || mtimeText(row.node)"
            @click="toggleDir(row.node)"
          >
            <AppIcon
              name="chevron-down"
              :size="11"
              :class="row.node.expanded ? '' : '-rotate-90'"
              class="shrink-0 text-[var(--c-text-faint)] transition-transform"
            />
            <AppIcon name="folder" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="truncate">{{ row.node.name }}</span>
            <span v-if="row.node.loading" class="ml-auto shrink-0 text-[10px] text-[var(--c-text-faint)]">读取中…</span>
            <span
              v-else-if="row.node.error"
              class="ml-auto max-w-[50%] shrink-0 truncate text-[10px] text-[var(--c-err-text)]"
            >{{ row.node.error }}</span>
          </button>
          <button
            v-else
            class="flex w-full items-center gap-1.5 rounded-md py-1 pr-1.5 text-left text-xs text-[var(--c-text-dim)] hover:bg-[var(--c-surface-soft)]"
            :style="{ paddingLeft: `${8 + row.depth * 14}px` }"
            :title="mtimeText(row.node)"
            @click="openFile(row.node)"
          >
            <!-- 与目录行的折叠箭头占同宽，文件名对齐目录名 -->
            <span class="h-[11px] w-[11px] shrink-0"></span>
            <AppIcon name="file" :size="13" class="shrink-0 text-[var(--c-text-faint)]" />
            <span class="truncate">{{ row.node.name }}</span>
          </button>
        </li>
      </ul>
    </div>
  </section>
</template>
