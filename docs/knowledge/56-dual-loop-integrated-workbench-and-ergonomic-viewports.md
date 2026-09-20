# 56. 现代 AI IDE 双主轴一体化工作台与高人机工程学视口流转

> **所属分类**：UI/UX 人机工程学 / 桌面端布局架构 / 单向数据流 / 性能优化  
> **关联模块**：`frontend/src/App.vue`, `frontend/src/stores/workbench.ts`, `frontend/src/components/DiffWorkspace.vue`, `frontend/src/components/LeftDrawer.vue`, `frontend/src/components/ActivityBar.vue`  
> **核心标签**：`双主轴工作台` `双侧边栏歼灭` `拖拽分栏手柄` `视口控制组` `防失联AI胶囊` `16:9人机工程学`

---

## ① 知识点与问题背景 (Context & Problem Statement)

在桌面端 AI IDE 产品演进过程中，界面视图与工作区布局直接决定了开发者的编码心智与注意力流畅度。在此前的版本中，顶栏与主工作区存在四个深层次交互硬伤：

1. **心智模型混淆（Category Mistake）**：顶栏设置了 `[智能对话] | [双栏协同] | [文件与编辑器]` 三态文字分段按钮，将“工作内容实体”（对话 vs 编辑器）与“窗口布局形态”（双栏协同 Split View）混为一谈；
2. **严重的“失联感”与视觉剧烈跳跃**：当用户切到“文件与编辑器”时，对话组件被 `v-show` 物理隐藏。大模型后台流式思考、工具执行或生成 Diff 时，用户完全无法感知；切回“双栏协同”时，编辑器又突兀缩至固定 46vw，破坏阅读连贯性；
3. **“双侧边栏夹心”灾难 (Double Sidebar Trap)**：左侧已有 `ActivityBar` + `LeftDrawer`（放会话分支），右侧 `DiffWorkspace` 打开后，内部竟然又嵌套了一块 240px 宽的内部文件树与全盘检索面板。屏幕横向被切成 `[图标栏] + [会话列表] + [对话框] + [编辑器内文件树] + [Monaco 编辑器]`，严重挤压中央核心区，彻底背离 16:9 人机工程学；
4. **死板硬编码宽度与控制权碎片化**：双栏宽度被写死为 `w-[46vw]`，缺少拖拽中缝（Draggable Sash），用户无法根据屏幕自由缩放；且顶栏、ChatCockpit 顶栏、ActivityBar 多个按钮散落冲突，彼此状态打架。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 2.1 现代顶级 AI IDE 的双主轴工作台黄金法则
对标 Cursor、Windsurf、VS Code Copilot 等工业界标杆，AI Coding 工作台应严格区分 **“导航容器 (Navigation Drawer)”**、**“核心双轴舞台 (Dual-Loop Arena)”** 与 **“视口控制组 (Viewport Controls)”**：

```text
┌────┬──────────────────┬─────────────────────────────┬───┬───────────────────────────────────────────┐
│ A  │ LeftDrawer       │ ChatCockpit (弹性流)        │ S │ DiffWorkspace (Monaco + Diff 纯净工作台)  │
│ c  │ (270px 可折叠)   │                             │ a │ (动态 editorSplitPercent %, 默认 52%)     │
│ t  ├──────────────────┤ • 思考流折叠胶囊            │ s │                                           │
│ i  │ • 会话分支 (#chat)│ • 历史工具输出原位修剪      │ h │ • 多文件标签页 (OpenEditorTabs)           │
│ v  │ • 文件树 (#files) │ • 动态附件折叠气泡          │   │ • 差异统计与 Cherry-Pick Hunk 采纳        │
│ i  │ • GitOps (#git)  │ • 输入框与实时状态机        │ │ │ • 纯净全宽 Monaco Edit / Diff 视窗        │
│ t  │                  │                             │ │ │ • 全屏专注 (Alt+F) / 收起 (Ctrl+\)        │
│ y  │ Ctrl+B 一键折叠  │                             │ │ │                                           │
└────┴──────────────────┴─────────────────────────────┴───┴───────────────────────────────────────────┘
```

### 2.2 解耦原则与单向数据流
- **左侧抽屉职责归一**：文件资源管理器（Explorer）与全盘检索作为一等公民活动面板归位至左侧 `LeftDrawer`，与会话分支、GitOps 平级三态，由 ActivityBar 图标与 `Ctrl+B` 统一管控。编辑器只做纯粹的代码阅读与比对，彻底消灭嵌套内树；
- **视口状态机收敛**：顶栏不再提供语义混淆的内容切换，而是提供 **视口布局控制组 (Viewport Presets)**：
  - `chat`：纯对话专注，代码区隐藏，对话区居中；
  - `split`：双栏协同（默认），支持中缝拖拽比例记忆与双击 50/50 复位；
  - `editor`：代码全屏专注，对话区隐藏，但顶栏挂载 **防失联 AI Mini-Cockpit 呼吸指示胶囊**；
- **防失联零盲区保障 (Zero-Blindspot)**：在 `editor` 模式下，若模型处于流式输出（`isStreaming`）或待确认代码变更（`pendingDiffFiles`），顶栏动态渲染呼吸灯微胶囊，显示实时百分比与状态，点击 1 键切回双栏协同，彻底消除盲区。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 3.1 状态层重构 (`workbench.ts`)
引入响应式状态并持久化记忆分栏比例：

```ts
const activeActivity = ref<'chat' | 'files' | 'git'>('chat')
const isLeftDrawerOpen = ref(true)
const isDiffOpen = ref(true)
const workspaceView = ref<'chat' | 'split' | 'editor'>('split')

// 拖拽分栏比例 (20% - 80%)，默认 52%，从本地存储恢复
const savedSplit = localStorage.getItem('tiancode_editor_split_percent')
const editorSplitPercent = ref<number>(savedSplit ? Math.min(Math.max(Number(savedSplit), 20), 80) : 52)
const isResizingSplit = ref(false)

function resetSplitRatio() {
  editorSplitPercent.value = 50
  try { localStorage.setItem('tiancode_editor_split_percent', '50') } catch {}
  showToast('✓ 已复位分栏比例 (50/50)')
}

function startSplitResize(e: MouseEvent) {
  e.preventDefault()
  isResizingSplit.value = true

  const onMouseMove = (moveEvent: MouseEvent) => {
    const mainEl = document.getElementById('workbench-main-area')
    if (!mainEl) return
    const rect = mainEl.getBoundingClientRect()
    const currentDiffPx = rect.right - moveEvent.clientX
    let newPercent = (currentDiffPx / rect.width) * 100
    if (newPercent < 20) newPercent = 20
    if (newPercent > 80) newPercent = 80
    editorSplitPercent.value = Math.round(newPercent)
  }

  const onMouseUp = () => {
    isResizingSplit.value = false
    window.removeEventListener('mousemove', onMouseMove)
    window.removeEventListener('mouseup', onMouseUp)
    try { localStorage.setItem('tiancode_editor_split_percent', String(editorSplitPercent.value)) } catch {}
  }

  window.addEventListener('mousemove', onMouseMove)
  window.addEventListener('mouseup', onMouseUp)
}
```

### 3.2 顶栏人机工程学视口控制组与防失联微胶囊 (`App.vue`)
```html
<!-- 顶栏核心工作台视口控制组 (Viewport Controls) -->
<div style="--wails-draggable:no-drag" class="flex items-center gap-1.5">
  <div class="flex items-center p-0.5 bg-black/[0.05] rounded-xl text-xs font-medium">
    <button @click="s.setWorkspaceView('chat')" title="纯对话专注模式 (隐藏代码面板)">
      <svg .../><span class="ml-1">对话专注</span>
    </button>
    <button @click="s.setWorkspaceView('split')" title="双栏协同工作台 (Ctrl+\)">
      <svg .../><span class="ml-1">双栏协同</span>
    </button>
    <button @click="s.setWorkspaceView('editor')" title="代码全屏专注 (Alt+F)">
      <svg .../><span class="ml-1">代码全屏</span>
    </button>
  </div>

  <!-- 代码全屏模式下的防失联 AI 指示胶囊 (Zero-Blindspot) -->
  <template v-if="s.workspaceView === 'editor'">
    <button v-if="s.isStreaming" @click="s.setWorkspaceView('split')" class="animate-pulse ...">
      <span class="animate-spin text-[10px]">⚡</span><span>AI 生成中... 展开双栏</span>
    </button>
    <button v-else-if="s.pendingDiffFiles.length > 0" @click="s.setWorkspaceView('split')" class="animate-pulse ...">
      <span>📝</span><span>待确认变更 ({{ s.pendingDiffFiles.length }})</span>
    </button>
    <button v-else @click="s.setWorkspaceView('split')" class="...">
      <span>💬</span><span>展开 AI 对话</span>
    </button>
  </template>
</div>
```

### 3.3 拖拽中缝手柄 (`App.vue`)
```html
<div class="flex-1 flex overflow-hidden relative" id="workbench-main-area">
  <ChatCockpit v-show="s.workspaceView !== 'editor'" />

  <!-- 可拖拽分栏手柄 (Draggable Splitter Sash) -->
  <div
    v-if="s.workspaceView === 'split' && s.isDiffOpen"
    @mousedown="s.startSplitResize($event)"
    @dblclick="s.resetSplitRatio()"
    class="w-2 -ml-1 -mr-1 z-30 cursor-col-resize hover:bg-[#D96B27]/40 active:bg-[#D96B27] transition-colors relative group select-none flex items-center justify-center shrink-0"
    title="双击平分窗口 (50/50)，按住左右拖拽调整分栏比例"
  >
    <div class="w-0.5 h-7 rounded-full bg-black/20 group-hover:bg-[#D96B27] transition-colors"></div>
  </div>

  <DiffWorkspace />
</div>
```

### 3.4 全局快捷键沉浸映射 (`workbench.ts`)
```ts
if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'b') {
  e.preventDefault()
  toggleLeftDrawer()
}
if ((e.ctrlKey || e.metaKey) && (e.key === '\\' || e.key === '|')) {
  e.preventDefault()
  toggleEditorPanel()
}
if (e.altKey && e.key.toLowerCase() === 'f') {
  e.preventDefault()
  toggleEditorFullscreen()
}
```

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **避免多次拖拽监听器泄漏**：
   在 `startSplitResize` 中，`mousemove` 与 `mouseup` 必须在 `window` 上监听，并在 `mouseup` 触发后立即调用 `window.removeEventListener` 彻底解绑，防止高频事件堆积或拖拽卡死；
2. **百分比安全边界约束 (Clamping)**：
   计算分栏宽度百分比时，必须通过 `Math.min(Math.max(newPercent, 20), 80)` 硬性约束在 `[20%, 80%]` 之间，防止用户拖过头导致左侧或右侧组件宽度被压缩至 0 触发 Monaco Editor 或虚拟流白屏；
3. **单向单一控制源原则 (Single Control Authority)**：
   在顶栏视口组接管布局后，必须彻底清理散落在各子组件中的私有硬编码切换逻辑（如 `ChatCockpit` 顶栏多余的“收起代码面板”按钮），确保整个应用对 `workspaceView` 与 `isDiffOpen` 的控制统一收敛至 Store。
