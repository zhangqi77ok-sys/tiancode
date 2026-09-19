<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useWorkbenchStore } from '../stores/workbench'
import type { PackageNode } from '../core/wailsBridge'

const s = useWorkbenchStore()

// 画布缩放与平移状态
const zoomScale = ref(1.0)
const panX = ref(0)
const panY = ref(0)
const isDragging = ref(false)
const dragStartX = ref(0)
const dragStartY = ref(0)

// 选定符号用于雷达分析
const selectedBlastSymbol = ref('host.Registry.GetTool')

// 层级元数据
const layerDefinitions = [
  { id: 'entry', name: '应用入口层 (Entry & Launcher)' },
  { id: 'host', name: '宿主中枢与事件分发 (Host & Transport)' },
  { id: 'core', name: '业务微内核与回路 (Core Engine & Loop)' },
  { id: 'bus', name: '抽象总线与安全防线 (Registry & Safety Rail)' },
  { id: 'spec', name: '插件契约与协议 (Plugin Spec & V1)' },
  { id: 'tool', name: '热插拔工具实现 (Hotplug Operators)' },
  { id: 'other', name: '通用支撑组件 (Supporting Utilities)' }
]

// 过滤后的包列表
const filteredPackages = computed(() => {
  const report = s.architectureReport
  if (!report || !report.packages) return []

  return report.packages.filter((pkg) => {
    // 仅看违规节点
    if (s.architectureFilterViolationsOnly) {
      const hasViolation = report.edges.some(
        (e) => e.is_violation && (e.from === pkg.id || e.to === pkg.id)
      )
      if (!hasViolation) return false
    }
    // 关键字检索
    if (s.architectureSearchQuery) {
      const q = s.architectureSearchQuery.toLowerCase()
      const matchName = pkg.name.toLowerCase().includes(q)
      const matchPath = pkg.path.toLowerCase().includes(q)
      const matchSym = pkg.symbols.some((sym) => sym.name.toLowerCase().includes(q))
      if (!matchName && !matchPath && !matchSym) return false
    }
    return true
  })
})

// 计算节点在画布上的多列自适应流式排布 (4 列折行与动态层高)
interface NodePosition {
  pkg: PackageNode
  x: number
  y: number
  w: number
  h: number
}

interface ComputedLayer {
  id: string
  name: string
  y: number
  height: number
  count: number
}

const COLS = 4
const CARD_W = 230
const CARD_H = 86
const GAP_X = 28
const GAP_Y = 16
const LAYER_HEADER_H = 34
const LAYER_MARGIN_BOTTOM = 28
const PADDING_LEFT = 60
const START_TOP = 40

const computedLayout = computed(() => {
  const map: Record<string, NodePosition> = {}
  const layerLayouts: ComputedLayer[] = []
  const layerBuckets: Record<string, PackageNode[]> = {}

  layerDefinitions.forEach((l) => {
    layerBuckets[l.id] = []
  })

  filteredPackages.value.forEach((pkg) => {
    const lId = pkg.layer || 'other'
    if (!layerBuckets[lId]) layerBuckets[lId] = []
    layerBuckets[lId].push(pkg)
  })

  let currentY = START_TOP

  layerDefinitions.forEach((layer) => {
    const pkgs = layerBuckets[layer.id] || []
    const count = pkgs.length
    const rows = Math.max(1, Math.ceil(count / COLS))
    const layerHeight = LAYER_HEADER_H + (count > 0 ? rows * CARD_H + (rows - 1) * GAP_Y : 20)

    layerLayouts.push({
      id: layer.id,
      name: layer.name,
      y: currentY,
      height: layerHeight,
      count
    })

    const cardsStartY = currentY + LAYER_HEADER_H

    pkgs.forEach((pkg, index) => {
      const col = index % COLS
      const row = Math.floor(index / COLS)
      const x = PADDING_LEFT + col * (CARD_W + GAP_X)
      const y = cardsStartY + row * (CARD_H + GAP_Y)
      map[pkg.id] = { pkg, x, y, w: CARD_W, h: CARD_H }
    })

    currentY += layerHeight + LAYER_MARGIN_BOTTOM
  })

  const totalHeight = Math.max(1200, currentY + 120)
  const totalWidth = Math.max(1400, PADDING_LEFT + COLS * (CARD_W + GAP_X) + 120)

  return {
    nodePositions: map,
    layerLayouts,
    totalHeight,
    totalWidth
  }
})

const nodePositions = computed(() => computedLayout.value.nodePositions)
const computedLayers = computed(() => computedLayout.value.layerLayouts)
const svgCanvasWidth = computed(() => computedLayout.value.totalWidth)
const svgCanvasHeight = computed(() => computedLayout.value.totalHeight)

// 计算 SVG 贝塞尔曲线边
interface RenderEdge {
  from: string
  to: string
  d: string
  isViolation: boolean
  violationReason?: string
  isOutgoing: boolean
  isIncoming: boolean
}

const renderEdges = computed<RenderEdge[]>(() => {
  const report = s.architectureReport
  if (!report || !report.edges) return []

  const posMap = nodePositions.value
  const activeId = s.selectedArchitectureNode?.id
  const edges: RenderEdge[] = []

  report.edges.forEach((edge) => {
    const fromPos = posMap[edge.from]
    const toPos = posMap[edge.to]
    if (!fromPos || !toPos) return

    const isViolation = !!edge.is_violation
    const isOutgoing = edge.from === activeId
    const isIncoming = edge.to === activeId

    let x1 = fromPos.x + fromPos.w / 2
    let y1 = fromPos.y + fromPos.h
    let x2 = toPos.x + toPos.w / 2
    let y2 = toPos.y

    // 反向依赖从侧面引出
    let d = ''
    if (fromPos.y >= toPos.y) {
      x1 = fromPos.x + fromPos.w
      y1 = fromPos.y + fromPos.h / 2
      x2 = toPos.x + toPos.w
      y2 = toPos.y + toPos.h / 2
      d = `M ${x1} ${y1} C ${x1 + 80} ${y1}, ${x2 + 80} ${y2}, ${x2} ${y2}`
    } else {
      const midY = (y1 + y2) / 2
      d = `M ${x1} ${y1} C ${x1} ${midY}, ${x2} ${midY}, ${x2} ${y2}`
    }

    edges.push({
      from: edge.from,
      to: edge.to,
      d,
      isViolation,
      violationReason: edge.violation_reason,
      isOutgoing,
      isIncoming
    })
  })

  return edges
})

function selectPackage(pkg: PackageNode) {
  s.selectedArchitectureNode = pkg
}

// 画布平移交互
function onMouseDown(e: MouseEvent) {
  const target = e.target as HTMLElement
  if (target.closest('.interactive-card') || target.closest('button') || target.closest('input')) {
    return
  }
  isDragging.value = true
  dragStartX.value = e.clientX - panX.value
  dragStartY.value = e.clientY - panY.value
}

function onMouseMove(e: MouseEvent) {
  if (!isDragging.value) return
  panX.value = e.clientX - dragStartX.value
  panY.value = e.clientY - dragStartY.value
}

function onMouseUp() {
  isDragging.value = false
}

function zoomIn() {
  zoomScale.value = Math.min(2.0, Number((zoomScale.value + 0.15).toFixed(2)))
}

function zoomOut() {
  zoomScale.value = Math.max(0.4, Number((zoomScale.value - 0.15).toFixed(2)))
}

function resetView() {
  zoomScale.value = 1.0
  panX.value = 0
  panY.value = 0
}

function handleBlastTargetChange(e: Event) {
  const target = (e.target as HTMLSelectElement).value
  selectedBlastSymbol.value = target
  s.runBlastRadiusAnalysis(target)
}

function resolveTargetFilePath(relOrAbs: string): string {
  if (!relOrAbs) return ''
  const norm = relOrAbs.replace(/\\/g, '/')
  if (norm.startsWith('/') || /^[a-zA-Z]:\//.test(norm)) {
    return norm
  }
  const base = (s.architectureCurrentPath || s.workspacePath || '').replace(/\\/g, '/').replace(/\/+$/, '')
  return base ? `${base}/${norm}` : norm
}

// 符号与契约单击直跳 Monaco 并高亮定位行号
function jumpToSource(filePath: string, line?: number) {
  if (!filePath) return
  const fullPath = resolveTargetFilePath(filePath)
  s.openEditorTab(fullPath, 'edit', line)
  s.setWorkspaceView('split')
  s.closeArchitectureModal()
}

// 推荐回归单测一键拉起并实时流式运行
async function runAffectedTest(testPkg: string) {
  const cleanPkg = testPkg.replace(/^\.\//, '').replace(/\/$/, '')
  const cmd = `go test -v ./${cleanPkg}/...`
  s.closeArchitectureModal()
  await s.runTerminalCommand(cmd)
}

// 模块与项目选择菜单状态
const isProjectMenuOpen = ref(false)

const currentProjectDisplayName = computed(() => {
  if (s.isExternalArchitectureProject) {
    const p = (s.architectureCurrentPath || '').replace(/\\/g, '/')
    const parts = p.split('/').filter(Boolean)
    return parts[parts.length - 1] || '外部项目'
  }
  const current = (s.architectureCurrentPath || '').replace(/\\/g, '/').toLowerCase()
  const mod = s.availableGoModules.find((m) => m.path.replace(/\\/g, '/').toLowerCase() === current)
  if (mod) return mod.name
  return s.workspaceName || '当前工作区'
})

function onSelectModule(modPath: string) {
  isProjectMenuOpen.value = false
  s.switchArchitectureProject(modPath)
}

async function onPickExternal() {
  isProjectMenuOpen.value = false
  await s.pickExternalArchitectureProject()
}

function onResetWorkspace() {
  isProjectMenuOpen.value = false
  s.resetArchitectureToWorkspace()
}

onMounted(() => {
  if (!s.architectureCurrentPath) {
    s.architectureCurrentPath = s.workspacePath
  }
  s.loadWorkspaceGoModules()
  if (!s.architectureReport && !s.isArchitectureLoading) {
    s.scanArchitecture(s.architectureCurrentPath)
  }
})
</script>

<template>
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-xs font-sans select-none animate-in fade-in duration-150"
    @keydown.esc="s.closeArchitectureModal()"
    @click.self="s.closeArchitectureModal()"
    tabindex="-1"
  >
    <!-- 模态主窗体 (94vw x 88vh, 暖色基底) -->
    <div class="w-[94vw] max-w-[1340px] h-[88vh] bg-[#FAF8F5] rounded-2xl shadow-2xl border border-black/[0.1] flex flex-col overflow-hidden relative">

      <!-- ================= 顶栏：视图切换与工具组 ================= -->
      <header class="h-13 bg-[#FAF8F5] border-b border-black/[0.08] px-5 flex items-center justify-between shrink-0 z-30">
        <!-- 左侧标题与模块选择器 -->
        <div class="flex items-center gap-3">
          <div class="w-8 h-8 rounded-xl bg-[#D96B27]/10 flex items-center justify-center text-[#D96B27] font-bold text-sm border border-[#D96B27]/20">
            🏛️
          </div>
          <div>
            <div class="flex items-center gap-2 relative">
              <h2 class="font-bold text-sm text-[#18181B] tracking-tight">代码架构与依赖治理</h2>

              <!-- 项目 / 模块选择器下拉开关 -->
              <div class="relative">
                <button
                  @click="isProjectMenuOpen = !isProjectMenuOpen"
                  :class="[
                    'px-2 py-0.5 rounded-lg border text-xs font-mono flex items-center gap-1.5 cursor-pointer transition-all',
                    s.isExternalArchitectureProject
                      ? 'bg-amber-50 border-amber-300 text-amber-800 font-bold shadow-2xs'
                      : 'bg-white border-black/[0.1] text-[#18181B] hover:bg-black/[0.02]'
                  ]"
                  title="切换工作区子模块或打开外部项目"
                >
                  <span v-if="s.isExternalArchitectureProject">📂 外部:</span>
                  <span v-else>📍</span>
                  <span class="max-w-40 truncate">{{ currentProjectDisplayName }}</span>
                  <span class="text-[10px] text-[#71717A]">▾</span>
                </button>

                <!-- 下拉菜单 -->
                <div
                  v-if="isProjectMenuOpen"
                  class="absolute left-0 top-full mt-1.5 w-76 bg-white rounded-xl border border-black/[0.1] shadow-xl py-1.5 z-50 text-xs"
                >
                  <!-- 外部模式重置快捷按钮 -->
                  <div v-if="s.isExternalArchitectureProject" class="px-2 pb-1.5 mb-1.5 border-b border-black/[0.06]">
                    <button
                      @click="onResetWorkspace"
                      class="w-full text-left px-2.5 py-1.5 rounded-lg bg-amber-100/70 hover:bg-amber-100 text-amber-900 font-bold flex items-center justify-between cursor-pointer"
                    >
                      <span>↩ 切回当前活动工作区</span>
                      <span class="text-[10px] text-amber-700 font-mono">{{ s.workspaceName }}</span>
                    </button>
                  </div>

                  <!-- 工作区内部模块 (Monorepo) -->
                  <div class="px-3 py-1 text-[10px] font-bold text-[#A1A1AA] uppercase tracking-wider">
                    📦 工作区内部模块 (Monorepo)
                  </div>
                  <div class="max-h-40 overflow-y-auto space-y-0.5 px-1">
                    <div
                      v-for="mod in s.availableGoModules"
                      :key="mod.path"
                      @click="onSelectModule(mod.path)"
                      :class="[
                        'px-2.5 py-1.5 rounded-lg flex items-center justify-between cursor-pointer transition-all font-mono',
                        (s.architectureCurrentPath.replace(/\\/g, '/').toLowerCase() === mod.path.replace(/\\/g, '/').toLowerCase())
                          ? 'bg-[#D96B27]/10 text-[#D96B27] font-bold'
                          : 'hover:bg-black/[0.04] text-[#27272A]'
                      ]"
                    >
                      <span class="truncate">{{ mod.name }}</span>
                      <span v-if="mod.is_root" class="text-[9px] px-1 py-0.2 rounded bg-black/[0.06] text-[#71717A]">根</span>
                    </div>
                  </div>

                  <div class="my-1 border-t border-black/[0.06]"></div>

                  <!-- 浏览外部独立项目 -->
                  <div class="px-1">
                    <button
                      @click="onPickExternal"
                      class="w-full text-left px-2.5 py-1.5 rounded-lg hover:bg-black/[0.04] text-[#18181B] font-medium flex items-center gap-2 cursor-pointer"
                    >
                      <span class="text-sm">📁</span>
                      <span>浏览选取本地其他 Go 项目...</span>
                    </button>
                  </div>

                  <!-- 最近分析历史 -->
                  <div v-if="s.recentAnalysisProjects && s.recentAnalysisProjects.length > 0" class="mt-1 pt-1 border-t border-black/[0.06]">
                    <div class="px-3 py-1 text-[10px] font-bold text-[#A1A1AA] uppercase tracking-wider">
                      🕒 最近参考项目
                    </div>
                    <div class="max-h-28 overflow-y-auto space-y-0.5 px-1">
                      <div
                        v-for="proj in s.recentAnalysisProjects"
                        :key="proj"
                        @click="onSelectModule(proj)"
                        :class="[
                          'px-2.5 py-1 rounded-lg truncate text-[11px] font-mono cursor-pointer transition-all',
                          (s.architectureCurrentPath.replace(/\\/g, '/').toLowerCase() === proj.replace(/\\/g, '/').toLowerCase())
                            ? 'bg-amber-100/70 text-amber-900 font-bold'
                            : 'hover:bg-black/[0.04] text-[#52525B]'
                        ]"
                        :title="proj"
                      >
                        {{ proj }}
                      </div>
                    </div>
                  </div>
                </div>
              </div>

              <!-- 外部模式指示标签 -->
              <span
                v-if="s.isExternalArchitectureProject"
                class="text-[10px] px-2 py-0.5 rounded-full bg-amber-100 text-amber-800 font-mono font-bold border border-amber-300 flex items-center gap-1"
              >
                <span>⚠️ 外部参考模式</span>
                <button
                  @click="s.resetArchitectureToWorkspace"
                  class="ml-1 text-amber-900 hover:underline cursor-pointer font-normal"
                  title="切回当前工作区"
                >
                  [↩ 切回当前工程]
                </button>
              </span>
              <span v-else class="text-[10px] px-1.5 py-0.2 rounded-full bg-black/[0.05] text-[#71717A] font-mono">
                {{ s.architectureReport?.total_packages || 0 }} 模块 / {{ s.architectureReport?.total_symbols || 0 }} 符号
              </span>
            </div>
          </div>
        </div>

        <!-- 中间：视图选项卡 -->
        <nav class="flex items-center bg-black/[0.04] p-1 rounded-xl border border-black/[0.06] text-xs font-medium">
          <button
            @click="s.activeArchitectureView = 'dag'"
            :class="[
              'px-3 py-1.5 rounded-lg transition-all flex items-center gap-1.5 cursor-pointer',
              s.activeArchitectureView === 'dag' ? 'bg-white text-[#18181B] font-bold shadow-2xs' : 'text-[#71717A] hover:text-[#18181B]'
            ]"
          >
            <span>🏛️</span><span>分层依赖拓扑 (Layered DAG)</span>
          </button>
          <button
            @click="s.activeArchitectureView = 'matrix'"
            :class="[
              'px-3 py-1.5 rounded-lg transition-all flex items-center gap-1.5 cursor-pointer',
              s.activeArchitectureView === 'matrix' ? 'bg-white text-[#18181B] font-bold shadow-2xs' : 'text-[#71717A] hover:text-[#18181B]'
            ]"
          >
            <span>🧩</span><span>接口契约多态 (Contracts)</span>
          </button>
          <button
            @click="() => { s.activeArchitectureView = 'blast'; s.runBlastRadiusAnalysis(selectedBlastSymbol) }"
            :class="[
              'px-3 py-1.5 rounded-lg transition-all flex items-center gap-1.5 cursor-pointer',
              s.activeArchitectureView === 'blast' ? 'bg-white text-[#18181B] font-bold shadow-2xs' : 'text-[#71717A] hover:text-[#18181B]'
            ]"
          >
            <span>💥</span><span>改动影响面雷达 (Blast Radius)</span>
          </button>
        </nav>

        <!-- 右侧：工具按钮与关闭 -->
        <div class="flex items-center gap-2">
          <!-- 搜索输入框 -->
          <div class="relative w-36">
            <span class="absolute left-2.5 top-1/2 -translate-y-1/2 text-xs text-[#A1A1AA]">🔍</span>
            <input
              v-model="s.architectureSearchQuery"
              type="text"
              placeholder="过滤模块/符号..."
              class="w-full pl-7 pr-2.5 py-1 text-xs bg-white rounded-lg border border-black/[0.1] focus:outline-none focus:border-[#D96B27] focus:ring-1 focus:ring-[#D96B27]/20 transition-all font-mono"
            />
          </div>

          <!-- 违规守卫切换 -->
          <button
            @click="s.architectureFilterViolationsOnly = !s.architectureFilterViolationsOnly"
            :class="[
              'px-2.5 py-1 rounded-lg border text-xs font-medium flex items-center gap-1 cursor-pointer transition-all',
              s.architectureFilterViolationsOnly
                ? 'bg-red-50 border-red-300 text-red-700 font-bold'
                : 'bg-white border-black/[0.1] text-[#52525B] hover:bg-black/[0.02]'
            ]"
            title="铁律 7 架构违规检查"
          >
            <span>🛡️ 守卫</span>
            <span
              v-if="(s.architectureReport?.violation_count || 0) > 0"
              class="px-1 py-0.2 rounded text-[9px] bg-red-100 text-red-600 font-mono font-bold"
            >
              {{ s.architectureReport?.violation_count }} 违规
            </span>
            <span v-else class="px-1 py-0.2 rounded text-[9px] bg-emerald-100 text-emerald-700 font-mono font-bold">
              0 违规
            </span>
          </button>

          <!-- 注入 Agent 按钮 (防投毒物理守卫) -->
          <button
            @click="s.injectArchitectureContext(null)"
            :disabled="s.isExternalArchitectureProject"
            :class="[
              'px-2.5 py-1 rounded-lg text-xs font-medium flex items-center gap-1 transition-all',
              s.isExternalArchitectureProject
                ? 'bg-black/[0.04] text-[#A1A1AA] border border-black/[0.06] cursor-not-allowed opacity-60'
                : 'bg-[#D96B27]/10 hover:bg-[#D96B27]/20 border border-[#D96B27]/30 text-[#D96B27] cursor-pointer font-bold'
            ]"
            :title="s.isExternalArchitectureProject ? '当前为外部参考项目，已物理禁用注入 Agent，防止上下文投毒' : '将工程全局架构拓扑注入 Agent 提示词'"
          >
            <span>🤖</span>
            <span>注入 Agent</span>
          </button>

          <!-- 扫描与重建 -->
          <button
            @click="() => s.scanArchitecture(s.architectureCurrentPath)"
            :disabled="s.isArchitectureLoading"
            class="px-2.5 py-1 rounded-lg bg-white border border-black/[0.1] text-xs font-medium text-[#18181B] hover:bg-black/[0.02] cursor-pointer flex items-center gap-1"
            title="重新扫描当前项目 AST"
          >
            <span :class="{ 'animate-spin': s.isArchitectureLoading }">🔄</span>
            <span>扫描</span>
          </button>

          <!-- 显式关闭按钮 [X] (铁律 5) -->
          <button
            @click="s.closeArchitectureModal()"
            class="p-1 rounded-lg text-[#71717A] hover:bg-black/[0.05] hover:text-[#18181B] cursor-pointer transition-colors"
            title="关闭并重置回主工作区 (Esc)"
          >
            ✕
          </button>
        </div>
      </header>

      <!-- ================= 主体内容视窗 ================= -->
      <div class="flex-1 flex overflow-hidden relative">

        <!-- 视图 1：分层 DAG 拓扑 -->
        <main
          v-show="s.activeArchitectureView === 'dag'"
          class="flex-1 relative overflow-hidden bg-[#FAF8F5] cursor-default"
          @mousedown="onMouseDown"
          @mousemove="onMouseMove"
          @mouseup="onMouseUp"
        >
          <!-- 悬浮层级说明条 -->
          <div class="absolute left-5 top-3.5 z-20 flex items-center gap-2 text-[11px] bg-white/90 backdrop-blur-md px-3 py-1 rounded-xl border border-black/[0.08] shadow-2xs font-mono text-[#52525B]">
            <span class="text-[#D96B27] font-bold">流动方向:</span>
            <span>入口层 ➔ 宿主调度 ➔ 业务内核 ➔ 抽象总线 ➔ 契约协议 ➔ 插件算子</span>
            <span class="text-black/[0.15]">|</span>
            <span class="text-[#10B981] font-medium">🟢 单向依赖</span>
          </div>

          <!-- 缩放控制浮条 -->
          <div class="absolute left-5 bottom-4 z-20 flex items-center bg-white/95 backdrop-blur-md border border-black/[0.08] rounded-xl p-0.5 shadow-md text-xs font-mono text-[#52525B]">
            <button @click="zoomOut" class="w-6 h-6 flex items-center justify-center rounded hover:bg-black/[0.05] cursor-pointer font-bold">－</button>
            <span class="px-2 font-medium">{{ Math.round(zoomScale * 100) }}%</span>
            <button @click="zoomIn" class="w-6 h-6 flex items-center justify-center rounded hover:bg-black/[0.05] cursor-pointer font-bold">＋</button>
            <div class="w-px h-3.5 bg-black/[0.08] mx-1"></div>
            <button @click="resetView" class="px-1.5 h-6 flex items-center justify-center rounded hover:bg-black/[0.05] cursor-pointer">⛶ 复位</button>
          </div>

          <!-- 转换视口容器 (支持平移与缩放) -->
          <div
            class="absolute inset-0 w-full h-full origin-top-left"
            :style="{ transform: `translate(${panX}px, ${panY}px) scale(${zoomScale})` }"
          >
            <!-- 背景层级分割基准线与自适应高度 -->
            <div
              v-for="(layer, idx) in computedLayers"
              :key="layer.id"
              class="absolute left-6 right-6 border-b border-black/[0.06] pointer-events-none flex items-center justify-between text-[11px] font-mono text-[#71717A] pt-1 pb-0.5"
              :style="{ top: layer.y + 'px' }"
            >
              <div class="flex items-center gap-2">
                <span class="font-bold text-[#18181B]">{{ layer.name }}</span>
                <span class="px-1.5 py-0.2 rounded-full bg-black/[0.05] text-[9px] font-bold text-[#71717A]">
                  {{ layer.count }} 模块
                </span>
              </div>
              <span class="text-black/[0.2] font-semibold">Layer {{ idx + 1 }}</span>
            </div>

            <!-- SVG 贝塞尔依赖连线 (动态自适应宽高) -->
            <svg
              class="absolute inset-0 pointer-events-none z-0"
              :style="{ width: svgCanvasWidth + 'px', height: svgCanvasHeight + 'px' }"
            >
              <defs>
                <marker id="dag-arrow-normal" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto">
                  <polygon points="0 1, 7 3.5, 0 6" fill="#A1A1AA" opacity="0.6" />
                </marker>
                <marker id="dag-arrow-out" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto">
                  <polygon points="0 1, 7 3.5, 0 6" fill="#D96B27" />
                </marker>
                <marker id="dag-arrow-in" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto">
                  <polygon points="0 1, 7 3.5, 0 6" fill="#10B981" />
                </marker>
                <marker id="dag-arrow-vio" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto">
                  <polygon points="0 1, 7 3.5, 0 6" fill="#EF4444" />
                </marker>
              </defs>
              <g>
                <path
                  v-for="(edge, i) in renderEdges"
                  :key="'edge-' + i"
                  :d="edge.d"
                  fill="none"
                  :class="[
                    edge.isViolation
                      ? 'stroke-red-500 stroke-2 [stroke-dasharray:5,5] animate-pulse'
                      : edge.isOutgoing
                      ? 'stroke-[#D96B27] stroke-2'
                      : edge.isIncoming
                      ? 'stroke-[#10B981] stroke-2'
                      : 'stroke-zinc-300 stroke-1'
                  ]"
                  :marker-end="
                    edge.isViolation
                      ? 'url(#dag-arrow-vio)'
                      : edge.isOutgoing
                      ? 'url(#dag-arrow-out)'
                      : edge.isIncoming
                      ? 'url(#dag-arrow-in)'
                      : 'url(#dag-arrow-normal)'
                  "
                />
              </g>
            </svg>

            <!-- 模块卡片列表 -->
            <div
              v-for="item in nodePositions"
              :key="item.pkg.id"
              :id="'card-' + item.pkg.id"
              class="interactive-card absolute p-3 rounded-2xl border bg-white shadow-2xs cursor-pointer transition-all duration-150 flex flex-col justify-between"
              :class="[
                s.selectedArchitectureNode?.id === item.pkg.id
                  ? 'ring-2 ring-[#D96B27] border-[#D96B27] z-10 shadow-md scale-[1.02]'
                  : 'border-black/[0.08] hover:border-[#D96B27]/40 hover:shadow-sm'
              ]"
              :style="{ left: item.x + 'px', top: item.y + 'px', width: item.w + 'px', height: item.h + 'px' }"
              @click.stop="selectPackage(item.pkg)"
            >
              <div>
                <div class="flex items-center justify-between">
                  <span class="text-xs">
                    {{ item.pkg.layer === 'entry' ? '🚀' : item.pkg.layer === 'core' ? '⚙️' : item.pkg.layer === 'spec' ? '📜' : item.pkg.layer === 'tool' ? '🧩' : item.pkg.layer === 'bus' ? '🔌' : '🌐' }}
                  </span>
                  <span class="text-[9px] font-mono px-1.5 py-0.2 rounded bg-black/[0.04] text-[#71717A] font-bold">
                    {{ item.pkg.layer.toUpperCase() }}
                  </span>
                </div>
                <div class="font-bold text-xs text-[#18181B] font-mono mt-1 truncate" :title="item.pkg.path">
                  {{ item.pkg.path }}
                </div>
              </div>
              <div class="flex items-center justify-between text-[10px] text-[#71717A] pt-1.5 border-t border-black/[0.04]">
                <span class="font-mono">{{ item.pkg.symbols.length }} 符号</span>
                <span class="font-mono">{{ item.pkg.files }} 文件</span>
              </div>
            </div>
          </div>
        </main>

        <!-- 视图 2：接口契约多态矩阵 (Matrix View) -->
        <main
          v-show="s.activeArchitectureView === 'matrix'"
          class="flex-1 p-6 overflow-y-auto bg-[#FAF8F5] space-y-6"
        >
          <div class="max-w-5xl mx-auto space-y-4">
            <div class="flex items-center justify-between border-b border-black/[0.08] pb-3">
              <div>
                <h3 class="text-sm font-bold text-[#18181B] flex items-center gap-2">
                  <span>🧩</span><span>抽象接口契约与插件多态实现矩阵</span>
                </h3>
                <p class="text-xs text-[#71717A] mt-0.5">静态解析工作区内 Interface 声明与结构体方法集，检验 100% 依赖倒置合规度</p>
              </div>
              <span class="text-xs font-mono font-bold text-[#059669]">
                共发现 {{ s.architectureReport?.contracts.length || 0 }} 处契约定义
              </span>
            </div>

            <div v-if="(s.architectureReport?.contracts.length || 0) === 0" class="p-12 text-center text-[#71717A] text-xs">
              工作区内暂未检测到导出的 Interface 契约声明
            </div>

            <div v-else class="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div
                v-for="contract in s.architectureReport?.contracts"
                :key="contract.package + '::' + contract.interface_name"
                class="p-4 rounded-2xl bg-white border border-black/[0.08] shadow-2xs space-y-3"
              >
                <div class="border-b border-black/[0.06] pb-2">
                  <div class="flex items-center justify-between">
                    <span class="text-[9px] px-2 py-0.5 rounded bg-[#D96B27]/10 text-[#D96B27] font-mono font-bold">INTERFACE CONTRACT</span>
                    <span class="text-[11px] text-[#059669] font-mono font-bold">
                      {{ contract.implementations.length }} 处多态实现
                    </span>
                  </div>
                  <h4
                    @click="jumpToSource(contract.file)"
                    class="text-xs font-bold text-[#18181B] hover:text-[#D96B27] font-mono mt-1 cursor-pointer flex items-center gap-1.5 group"
                    title="单击跳转到接口定义源码"
                  >
                    <span>{{ contract.package }}.{{ contract.interface_name }}</span>
                    <span class="text-[10px] text-[#A1A1AA] group-hover:text-[#D96B27]">↗</span>
                  </h4>
                  <div
                    @click="jumpToSource(contract.file)"
                    class="text-[10px] text-[#A1A1AA] hover:text-[#D96B27] font-mono mt-0.5 cursor-pointer"
                  >
                    📄 {{ contract.file }}
                  </div>
                </div>

                <!-- 方法集 -->
                <div class="space-y-1">
                  <span class="text-[11px] font-bold text-[#71717A]">契约方法签名:</span>
                  <div class="p-2 rounded-xl bg-[#FAF8F5] border border-black/[0.04] space-y-0.5 font-mono text-[11px] text-[#52525B]">
                    <div v-for="m in contract.methods" :key="m" class="flex items-center gap-1.5">
                      <span class="text-[#D96B27]">›</span><span>{{ m }}()</span>
                    </div>
                  </div>
                </div>

                <!-- 实现者列表 (支持单击直跳源码) -->
                <div class="space-y-1">
                  <span class="text-[11px] font-bold text-[#71717A]">结构体实现绑定:</span>
                  <div v-if="contract.implementations.length === 0" class="text-[11px] text-[#A1A1AA] italic">
                    暂无结构体完整实现该接口
                  </div>
                  <div v-else class="space-y-1">
                    <div
                      v-for="impl in contract.implementations"
                      :key="impl.package + '::' + impl.struct_name"
                      @click="jumpToSource(impl.file)"
                      class="p-1.5 rounded-lg bg-[#FAF8F5] hover:bg-[#D96B27]/10 hover:border-[#D96B27]/30 border border-black/[0.04] flex items-center justify-between text-xs font-mono cursor-pointer group transition-all"
                      :title="'单击直达 ' + impl.file + ' 源码实现'"
                    >
                      <div class="flex items-center gap-1.5 truncate">
                        <span class="font-bold text-[#18181B] group-hover:text-[#D96B27]">{{ impl.package }}.{{ impl.struct_name }}</span>
                        <span v-if="impl.file" class="text-[10px] text-[#A1A1AA] group-hover:text-[#D96B27]">({{ impl.file.split('/').pop() }}) ↗</span>
                      </div>
                      <span class="text-[9px] px-1.5 py-0.2 rounded bg-emerald-100 text-emerald-700 font-bold shrink-0">
                        {{ impl.status === 'compliant' ? '🟢 100% 遵从' : '🟡 部分实现' }}
                      </span>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </main>

        <!-- 视图 3：改动影响面雷达 (Blast Radius) -->
        <main
          v-show="s.activeArchitectureView === 'blast'"
          class="flex-1 p-6 overflow-y-auto bg-[#FAF8F5] space-y-6"
        >
          <div class="max-w-4xl mx-auto space-y-4">
            <div class="flex items-center justify-between border-b border-black/[0.08] pb-3">
              <div>
                <h3 class="text-sm font-bold text-[#18181B] flex items-center gap-2">
                  <span>💥</span><span>符号改动影响面雷达 (Refactoring Blast Radius)</span>
                </h3>
                <p class="text-xs text-[#71717A] mt-0.5">在改写核心代码前，毫秒级预测波及调用方与必跑回归测试</p>
              </div>
            </div>

            <!-- 选择目标符号 -->
            <div class="bg-white p-4 rounded-2xl border border-black/[0.08] shadow-2xs space-y-4">
              <div class="flex items-center gap-3">
                <label class="text-xs font-bold text-[#71717A]">改动目标符号:</label>
                <select
                  :value="selectedBlastSymbol"
                  @change="handleBlastTargetChange"
                  class="px-3 py-1.5 text-xs bg-[#FAF8F5] border border-black/[0.1] rounded-lg font-mono focus:outline-none"
                >
                  <option value="host.Registry.GetTool">internal/host.Registry.GetTool (核心算子路由)</option>
                  <option value="loop.Engine.ExecuteStep">internal/core/loop.Engine (单一执行内核)</option>
                  <option value="rail.Safety.OnBeforeAct">plugins/rail/safety.SafetyRail (物理安全防线)</option>
                  <option value="git.Tool.StageFile">plugins/tool/git.Tool.StageFile (Git 暂存写入)</option>
                </select>
                <span v-if="s.isBlastRadiusLoading" class="text-xs text-[#D96B27] animate-spin">⏳</span>
              </div>

              <!-- 分析结果面板 -->
              <div v-if="s.blastRadiusReport" class="space-y-3 pt-2">
                <div
                  :class="[
                    'p-3.5 rounded-xl border flex items-center justify-between text-xs font-mono',
                    s.blastRadiusReport.risk_level === 'CRITICAL' ? 'bg-red-50 border-red-300 text-red-800' :
                    s.blastRadiusReport.risk_level === 'HIGH' ? 'bg-amber-50 border-amber-300 text-amber-800' :
                    'bg-emerald-50 border-emerald-300 text-emerald-800'
                  ]"
                >
                  <div>
                    <div class="font-bold">评估等级: {{ s.blastRadiusReport.risk_level }}</div>
                    <div class="text-[11px] mt-0.5 font-sans">{{ s.blastRadiusReport.suggestion }}</div>
                  </div>
                  <span class="text-xl">⚠️</span>
                </div>

                <!-- 符号级精准调用点雷达 (Call Sites) -->
                <div class="p-3.5 rounded-xl bg-[#FAF8F5] border border-black/[0.06] space-y-2.5">
                  <div class="flex items-center justify-between">
                    <span class="font-bold text-xs text-[#71717A] flex items-center gap-1.5">
                      <span>🎯</span>
                      <span>符号引用与精准调用点 ({{ s.blastRadiusReport.call_sites?.length || 0 }} 处定位):</span>
                    </span>
                    <span class="text-[10px] font-mono text-[#A1A1AA]">AST Selector & Ident</span>
                  </div>

                  <div v-if="!s.blastRadiusReport.call_sites || s.blastRadiusReport.call_sites.length === 0" class="text-[#A1A1AA] text-xs italic py-2">
                    暂未在工作区内扫描到该符号的具体调用语句或实例化点
                  </div>
                  <div v-else class="space-y-1.5 max-h-56 overflow-y-auto pr-1">
                    <div
                      v-for="(cs, idx) in s.blastRadiusReport.call_sites"
                      :key="cs.file + ':' + cs.line + ':' + idx"
                      @click="jumpToSource(cs.file, cs.line)"
                      class="p-2 rounded-xl bg-white border border-black/[0.06] hover:border-[#D96B27]/40 hover:shadow-2xs cursor-pointer group transition-all font-mono text-xs flex flex-col gap-1"
                      title="单击直达代码调用行"
                    >
                      <div class="flex items-center justify-between text-[11px]">
                        <div class="flex items-center gap-1.5 truncate">
                          <span class="text-[#D96B27] font-bold">📄 {{ cs.file }}</span>
                          <span class="text-[#A1A1AA]">in</span>
                          <span class="text-[#18181B] font-semibold">{{ cs.function }}()</span>
                        </div>
                        <span class="px-1.5 py-0.2 rounded bg-black/[0.04] text-[#71717A] font-bold group-hover:text-[#D96B27] group-hover:bg-[#D96B27]/10 shrink-0">
                          Line {{ cs.line }} ↗
                        </span>
                      </div>
                      <div v-if="cs.snippet" class="text-[11px] text-[#52525B] bg-[#FAF8F5] p-1.5 rounded-md border border-black/[0.03] truncate font-mono">
                        {{ cs.snippet }}
                      </div>
                    </div>
                  </div>
                </div>

                <div class="grid grid-cols-2 gap-4 text-xs font-mono">
                  <div class="p-3 rounded-xl bg-[#FAF8F5] border border-black/[0.06] space-y-2">
                    <span class="font-bold text-[#71717A]">直接引入包 (Direct Callers):</span>
                    <div v-if="s.blastRadiusReport.direct_callers.length === 0" class="text-[#A1A1AA] text-[11px]">无直接上游引入</div>
                    <div v-else class="space-y-1 max-h-48 overflow-y-auto pr-1">
                      <div
                        v-for="c in s.blastRadiusReport.direct_callers"
                        :key="c"
                        class="p-1.5 rounded bg-white border border-black/[0.04] text-[#18181B] truncate"
                        :title="c"
                      >
                        ↳ {{ c }}
                      </div>
                    </div>
                  </div>

                  <div class="p-3 rounded-xl bg-[#FAF8F5] border border-black/[0.06] space-y-2">
                    <div class="flex items-center justify-between">
                      <span class="font-bold text-[#71717A]">推荐必跑回归测试:</span>
                      <span class="text-[10px] text-[#A1A1AA] font-mono">一键运行</span>
                    </div>
                    <div v-if="s.blastRadiusReport.affected_tests.length === 0" class="text-[#A1A1AA] text-[11px]">无对应单测</div>
                    <div v-else class="space-y-1.5 max-h-48 overflow-y-auto pr-1">
                      <div
                        v-for="t in s.blastRadiusReport.affected_tests"
                        :key="t"
                        class="p-2 rounded-xl bg-white border border-black/[0.06] flex items-center justify-between gap-2 shadow-2xs"
                      >
                        <span class="text-xs font-mono text-[#059669] truncate font-bold" :title="'go test -v ./' + t + '/...'">
                          go test ./{{ t }}/...
                        </span>
                        <button
                          @click="runAffectedTest(t)"
                          class="px-2 py-1 rounded-lg bg-emerald-50 hover:bg-emerald-100 text-emerald-700 border border-emerald-200 text-xs font-mono font-bold flex items-center gap-1 cursor-pointer transition-all shrink-0"
                          title="在底层终端抽屉立即执行该包的单元测试"
                        >
                          <span>▶</span>
                          <span>运行</span>
                        </button>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </main>

        <!-- ================= 右侧：模块属性检查抽屉 (Inspector) ================= -->
        <aside class="w-88 bg-white border-l border-black/[0.08] flex flex-col justify-between overflow-y-auto shrink-0 z-20">
          <div class="p-4 space-y-4">
            <!-- 模块基本信息 -->
            <div class="pb-3 border-b border-black/[0.06]">
              <div class="flex items-center justify-between">
                <span class="text-[10px] px-2 py-0.5 rounded-md font-mono font-bold bg-[#D96B27]/10 text-[#D96B27]">
                  {{ s.selectedArchitectureNode?.layer_name.split(' ')[0] || '未知层级' }}
                </span>
                <span class="text-[10px] px-1.5 py-0.5 rounded-md font-mono font-bold bg-[#10B981]/10 text-[#059669]">
                  🟢 契约合规
                </span>
              </div>
              <h3 class="text-sm font-bold text-[#18181B] font-mono mt-1.5 truncate" :title="s.selectedArchitectureNode?.path">
                {{ s.selectedArchitectureNode?.path || '未选择模块' }}
              </h3>
              <div class="text-[11px] text-[#71717A] mt-0.5">Package: {{ s.selectedArchitectureNode?.name }}</div>
            </div>

            <!-- 关键指标 -->
            <div class="grid grid-cols-3 gap-2 text-center">
              <div class="p-2 rounded-xl bg-[#FAF8F5] border border-black/[0.04]">
                <div class="text-[9px] text-[#71717A]">源码文件</div>
                <div class="text-xs font-bold font-mono text-[#18181B] mt-0.5">{{ s.selectedArchitectureNode?.files || 0 }}</div>
              </div>
              <div class="p-2 rounded-xl bg-[#FAF8F5] border border-black/[0.04]">
                <div class="text-[9px] text-[#71717A]">导出符号</div>
                <div class="text-xs font-bold font-mono text-[#18181B] mt-0.5">{{ s.selectedArchitectureNode?.symbols.length || 0 }}</div>
              </div>
              <div class="p-2 rounded-xl bg-[#FAF8F5] border border-black/[0.04]">
                <div class="text-[9px] text-[#71717A]">出度 / 入度</div>
                <div class="text-xs font-bold font-mono text-[#D96B27] mt-0.5">
                  {{ s.selectedArchitectureNode?.imports.length || 0 }} / {{ s.selectedArchitectureNode?.imported_by.length || 0 }}
                </div>
              </div>
            </div>

            <!-- 导出的符号列表 (支持单击直达 Monaco 编辑器) -->
            <div>
              <div class="text-xs font-bold text-[#71717A] mb-1.5 flex items-center justify-between">
                <span>AST 导出实体 ({{ s.selectedArchitectureNode?.symbols.length || 0 }})</span>
                <span class="text-[9px] font-mono text-[#A1A1AA]">单击直跳 Monaco</span>
              </div>
              <div class="space-y-1 max-h-40 overflow-y-auto pr-1">
                <div
                  v-for="sym in s.selectedArchitectureNode?.symbols"
                  :key="sym.name + ':' + sym.line"
                  @click="jumpToSource(sym.file || (s.selectedArchitectureNode?.path + '/' + sym.name + '.go'), sym.line)"
                  class="p-1.5 rounded bg-[#FAF8F5] hover:bg-[#D96B27]/10 hover:border-[#D96B27]/30 border border-black/[0.04] text-xs font-mono flex items-center justify-between cursor-pointer group transition-all"
                  :title="'单击直跳 ' + sym.file + ' 第 ' + sym.line + ' 行'"
                >
                  <div class="flex items-center gap-1 truncate">
                    <span class="text-[9px] text-[#D96B27] uppercase">{{ sym.kind }}</span>
                    <span class="font-bold text-[#18181B] group-hover:text-[#D96B27]">{{ sym.name }}</span>
                  </div>
                  <span class="text-[9px] text-[#A1A1AA] group-hover:text-[#D96B27] font-bold">L{{ sym.line }} ↗</span>
                </div>
              </div>
            </div>

            <!-- 依赖与被依赖 -->
            <div class="space-y-2.5">
              <div>
                <div class="text-xs font-bold text-[#71717A] mb-1">
                  ⬇️ 依赖的内部包 ({{ s.selectedArchitectureNode?.imports.length || 0 }})
                </div>
                <div v-if="(s.selectedArchitectureNode?.imports.length || 0) === 0" class="text-[11px] text-[#A1A1AA] italic">
                  无内部依赖 (底层独立模块)
                </div>
                <div v-else class="space-y-1 max-h-24 overflow-y-auto">
                  <div
                    v-for="imp in s.selectedArchitectureNode?.imports"
                    :key="imp"
                    class="p-1 rounded bg-[#FAF8F5] text-xs font-mono text-[#18181B]"
                  >
                    ➔ {{ imp }}
                  </div>
                </div>
              </div>

              <div>
                <div class="text-xs font-bold text-[#71717A] mb-1">
                  ⬆️ 被哪些模块依赖 ({{ s.selectedArchitectureNode?.imported_by.length || 0 }})
                </div>
                <div v-if="(s.selectedArchitectureNode?.imported_by.length || 0) === 0" class="text-[11px] text-[#A1A1AA] italic">
                  顶层入口 (无上游引入方)
                </div>
                <div v-else class="space-y-1 max-h-24 overflow-y-auto">
                  <div
                    v-for="by in s.selectedArchitectureNode?.imported_by"
                    :key="by"
                    class="p-1 rounded bg-[#FAF8F5] text-xs font-mono text-[#18181B]"
                  >
                    ⬅ {{ by }}
                  </div>
                </div>
              </div>
            </div>

            <!-- 架构守卫提示 -->
            <div class="p-3 rounded-xl bg-[#F4EFEA]/60 border border-black/[0.06] text-xs space-y-1">
              <div class="flex items-center justify-between text-[#18181B] font-bold">
                <span>📜 架构守卫原则</span>
                <span class="text-[9px] px-1 py-0.2 rounded bg-white text-[#D96B27] font-mono">铁律 7</span>
              </div>
              <p class="text-[11px] text-[#52525B] leading-relaxed">
                所有工具执行必须经由 host.Registry 动态注册；微内核层禁止直接 import 具体 plugins/tool 实现。
              </p>
            </div>
          </div>

          <!-- 底部操作按钮 -->
          <div class="p-4 border-t border-black/[0.08] bg-[#FAF8F5] space-y-2">
            <button
              @click="s.injectArchitectureContext(s.selectedArchitectureNode)"
              :disabled="s.isExternalArchitectureProject"
              :class="[
                'w-full py-2 px-3 rounded-xl text-xs font-bold flex items-center justify-center gap-1.5 transition-all',
                s.isExternalArchitectureProject
                  ? 'bg-black/[0.08] text-[#A1A1AA] cursor-not-allowed border border-black/[0.05]'
                  : 'bg-[#D96B27] hover:bg-[#B8551B] text-white cursor-pointer shadow-xs'
              ]"
              :title="s.isExternalArchitectureProject ? '当前为外部参考项目，仅活动工作区支持注入 Agent 会话，防止上下文投毒' : '将本模块架构约束注入对话'"
            >
              <span>{{ s.isExternalArchitectureProject ? '🛡️' : '💬' }}</span>
              <span>{{ s.isExternalArchitectureProject ? '外部项目模式 (已阻断注入)' : '将本模块架构约束注入对话' }}</span>
            </button>
            <div v-if="s.isExternalArchitectureProject" class="text-[10px] text-amber-600/90 text-center font-mono">
              ⚠️ 防投毒守卫已激活：禁止跨工程注入
            </div>
          </div>
        </aside>
      </div>
    </div>
  </div>
</template>
