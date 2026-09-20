<template>
  <div>
    <div
      @click="handleClick"
      @contextmenu.prevent="s.openFileContextMenu($event, node)"
      :style="{ paddingLeft: `${depth * 10 + 6}px` }"
      :class="[
        'py-1 pr-1.5 rounded hover:bg-white cursor-pointer flex items-center justify-between text-[11px] font-mono group transition-colors',
        !node.is_dir && s.activeDiffFile === node.path ? 'bg-white font-bold text-[#D96B27] shadow-2xs' : 'text-[#27272A]'
      ]"
      :title="node.path"
    >
      <div class="flex items-center gap-1 min-w-0 flex-1 truncate">
        <span v-if="node.is_dir" class="text-[9px] text-[#71717A] w-3 text-center shrink-0">
          {{ isExpanded ? '▼' : '▶' }}
        </span>
        <span class="text-xs shrink-0">{{ node.is_dir ? '📁' : '📄' }}</span>
        <span class="truncate">{{ node.name }}</span>
      </div>

      <!-- Git 变更状态角标 -->
      <span
        v-if="gitBadge"
        :class="gitBadge.color"
        class="text-[9px] font-bold font-mono px-1 rounded bg-black/[0.04] shrink-0 ml-1"
        :title="'Git 状态: ' + gitBadge.code"
      >
        {{ gitBadge.code }}
      </span>
    </div>

    <!-- 递归子节点渲染 (支持按需懒加载与任意深度展开) -->
    <div v-if="node.is_dir && isExpanded">
      <div v-if="node.loading" class="text-[10px] text-[#71717A] py-1 font-mono" :style="{ paddingLeft: `${(depth + 1) * 10 + 6}px` }">
        加载中...
      </div>
      <template v-else-if="node.children && node.children.length > 0">
        <FileTreeNode
          v-for="child in node.children"
          :key="child.path"
          :node="child"
          :depth="depth + 1"
        />
      </template>
      <div v-else-if="node.loaded" class="text-[10px] text-[#71717A]/60 py-0.5 font-mono italic" :style="{ paddingLeft: `${(depth + 1) * 10 + 6}px` }">
        (空目录)
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useWorkbenchStore, type FileNode } from '../stores/workbench'

const props = defineProps<{
  node: FileNode
  depth: number
}>()

const s = useWorkbenchStore()

const isExpanded = computed(() => {
  return !!s.expandedFolders[props.node.path]
})

const gitBadge = computed(() => {
  return s.gitStatusMap[props.node.path]
})

function handleClick() {
  s.handleFileClick(props.node)
}
</script>
