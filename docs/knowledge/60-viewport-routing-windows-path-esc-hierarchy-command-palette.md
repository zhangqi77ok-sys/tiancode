# 60. 视口路由、Windows路径标准化、Esc层级补全与Command Palette合规

## ① 知识点与问题背景

第四轮 Grill-Me 深度审查发现 6 项已有功能缺陷，全部针对交互正确性与铁律合规，无新增功能：

1. 文件树点击在"对话专注"(chat) 视口模式下编辑器不可见
2. Windows 反斜杠路径导致编辑器 Tab 标题显示完整路径
3. `[⚡ 应用至编辑器]` 无活动文件时静默失败（用户看不到任何效果）
4. 全局 Esc 跳过 9 个 pending 文件操作弹窗（铁律 5 违规）
5. ChatCockpit 头部模型名冗余显示（同一信息出现两次）
6. Command Palette 无标题行、无 [X] 按钮、背景色错误（铁律 5 违规）

---

## ② 核心原理与知识内容

### 视口三态互斥模型

`workspaceView` 有三个状态：`'chat'` | `'split'` | `'editor'`。

`DiffWorkspace.vue` 的可见条件是：
```html
v-show="s.isDiffOpen && s.workspaceView !== 'chat'"
```

任何期望显示编辑器的操作（文件树点击、搜索结果跳转、代码应用）都 **必须同时满足**：
- `isDiffOpen = true`
- `workspaceView !== 'chat'`

以前 `openEditorTab` 只设置 `isDiffOpen = true`，不管 `workspaceView`，导致 chat 模式下文件点击毫无反馈。

### Windows 路径反斜杠

前端代码 `filePath.split('/').pop()` 只识别正斜杠。Windows 传来的路径形如 `internal\core\loop\strategy.go`，用 `split('/')` 返回整串作为 title。
修复：`filePath.replace(/\\/g, '/')` 先标准化。

### 全局 Esc 优先级链设计

`handleGlobalKeydown` 按优先级链决定 Esc 的关闭目标。新增的文件操作弹窗都通过 `@keydown.esc` 属性在模板层处理，**但该属性要求元素本身聚焦**。

全局键盘监听器（`window.addEventListener('keydown', handleGlobalKeydown)`）必须统一处理所有弹窗，不能依赖模板层的 `@keydown.esc`。

新增优先级顺序：
```
mentionOpen → isCommandPaletteOpen → tabContextMenu → fileContextMenu
→ pendingCloseTab → pendingDeleteSessionId → pendingTagSession → pendingRenameSession
→ pendingCreateFile → pendingCreateFolder → pendingRenamePath → pendingDeletePath
→ pendingChoice → pendingConfirm → isChannelModalOpen → ...
```

---

## ③ 标准解决方案与实操步骤

### 修复1+2: openEditorTab 视口自动切换 + 路径标准化

```typescript
function openEditorTab(filePath: string, viewMode: 'edit' | 'diff' = 'edit', line?: number) {
  if (!filePath) return
  // 标准化 Windows 反斜杠路径
  const normalizedPath = filePath.replace(/\\/g, '/')
  // ...
  activeDiffFile.value = normalizedPath
  isDiffOpen.value = true
  editorView.value = viewMode
  // 在"对话专注"模式下打开文件，自动切换到双栏协同
  if (workspaceView.value === 'chat') {
    workspaceView.value = 'split'
  }
}
```

### 修复4: 全局 Esc 补全

```typescript
// 在 handleGlobalKeydown e.key === 'Escape' 分支中，优先级 1.5:
if (fileContextMenu.value) { fileContextMenu.value = null; return }
if (pendingCloseTab.value) { pendingCloseTab.value = null; return }
if (pendingDeleteSessionId.value) { pendingDeleteSessionId.value = null; return }
if (pendingTagSession.value) { pendingTagSession.value = null; return }
if (pendingRenameSession.value) { pendingRenameSession.value = null; return }
if (pendingCreateFile.value?.isOpen) { pendingCreateFile.value.isOpen = false; return }
if (pendingCreateFolder.value?.isOpen) { pendingCreateFolder.value.isOpen = false; return }
if (pendingRenamePath.value?.isOpen) { pendingRenamePath.value.isOpen = false; return }
if (pendingDeletePath.value?.isOpen) { pendingDeletePath.value.isOpen = false; return }
```

### 修复3: code-apply-btn 正确行为

```typescript
if (!s.activeDiffFile) {
  s.showToast('⚠️ 请先从左侧文件树中点击目标文件，再应用代码')
  return
}
s.editorContent = rawCode
s.markEditorDirty()
s.editorView = 'edit'  // 确保切到编辑视图
s.setWorkspaceView('split')
```

### 修复6: Command Palette 铁律5合规

```html
<div class="fixed inset-0 z-[60] flex items-center justify-center bg-black/40" @click.self="...">
  <div class="bg-[#FAF8F5] rounded-2xl shadow-2xl ...">
    <!-- 标题行 -->
    <div class="h-10 bg-[#F4EFEA] border-b ...">
      <span>快速跳转</span>
      <button @click="s.isCommandPaletteOpen = false" title="关闭 (Esc)">✕</button>
    </div>
    <!-- 搜索输入 (也绑定 @keydown.esc) -->
    <input @keydown.esc.prevent="s.isCommandPaletteOpen = false" .../>
  </div>
</div>
```

---

## ④ 避坑指南与最佳实践

1. **视口模式三态意识**：所有打开编辑器的操作都必须检查并修正 `workspaceView`，不能只设 `isDiffOpen`。
2. **Windows 路径**：前端接收 Go 传来的路径时，始终先 `replace(/\\/g, '/')` 再处理。
3. **全局 Esc 中心化**：不要在 Vue 模板用 `@keydown.esc` 作为 Esc 的主要响应机制，必须在 `handleGlobalKeydown` 统一注册所有弹窗状态。模板层的 `@keydown.esc` 仅作辅助（需要聚焦才能触发）。
4. **铁律5 弹窗检查清单**：每新增一个弹窗状态（`pendingXxx`），必须同步在 `handleGlobalKeydown` 中注册 Esc 处理。
5. **showToast 已通过 store return 导出**（L3099），直接使用 `s.showToast(msg)` 即可。
