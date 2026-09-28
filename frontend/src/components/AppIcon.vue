<script setup lang="ts">
// 内联 SVG 图标集：stroke 跟随 currentColor，颜色由使用处的 text-* 类决定。
// 为什么不用图标库：仓库依赖纪律（最少依赖、离线可构建），所需图标少且形态简单。
type IconName =
  | 'plus'
  | 'x'
  | 'check'
  | 'pencil'
  | 'trash'
  | 'folder'
  | 'shield'
  | 'send'
  | 'stop'
  | 'chevron-down'
  | 'download'
  | 'sliders'
  | 'alert'
  | 'refresh'
  | 'menu'
  | 'copy'

withDefaults(defineProps<{ name: IconName; size?: number }>(), { size: 16 })

// 每个图标由一条或多条 path 组成（viewBox 24，线性风格）
const PATHS: Record<IconName, string[]> = {
  plus: ['M12 5v14', 'M5 12h14'],
  x: ['M18 6 6 18', 'm6 6 12 12'],
  check: ['M20 6 9 17l-5-5'],
  'chevron-down': ['m6 9 6 6 6-6'],
  pencil: ['M12 20h9', 'M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z'],
  trash: [
    'M3 6h18',
    'M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2',
    'M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6',
  ],
  folder: ['M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-8L9.6 3.6A2 2 0 0 0 8 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z'],
  shield: ['M12 22s8-3.5 8-10V5l-8-3-8 3v7c0 6.5 8 10 8 10Z'],
  send: ['M22 2 11 13', 'M22 2 15 22l-4-9-9-4Z'],
  stop: ['M7 7h10v10H7z'],
  download: ['M12 3v12', 'm8 11 4 4 4-4', 'M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2'],
  sliders: ['M3 6h8', 'M15 6h6', 'M13 4v4', 'M3 12h12', 'M19 12h2', 'M17 10v4', 'M3 18h4', 'M11 18h10', 'M9 16v4'],
  alert: [
    'm21.73 18-8-14a2 2 0 0 0-3.46 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3',
    'M12 9v4',
    'M12 17h.01',
  ],
  refresh: ['M21 12a9 9 0 1 1-2.64-6.36', 'M21 3v6h-6'],
  menu: ['M4 6h16', 'M4 12h16', 'M4 18h16'],
  copy: [
    'M10 8h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-8a2 2 0 0 1-2-2v-8a2 2 0 0 1 2-2Z',
    'M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2',
  ],
}
</script>

<template>
  <svg
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.8"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path v-for="(d, i) in PATHS[name]" :key="i" :d="d" />
  </svg>
</template>
