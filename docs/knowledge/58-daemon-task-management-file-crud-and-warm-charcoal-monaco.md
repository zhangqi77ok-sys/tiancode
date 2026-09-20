# 知识点 58: 后台长驻守护进程管控、受控文件树 CRUD、会话重命名与暖炭黑 Monaco 编辑器全域治理

> **归档时间**：2026-09-21  
> **涉及模块**：终端工具链 (`plugins/tool/terminal`)、微内核沙箱 (`internal/core/sandbox`)、会话引擎 (`internal/session`)、Monaco 编辑器 (`frontend/src/core/monacoEnv.ts`)、工作台状态与抽屉 (`LeftDrawer.vue` / `TerminalDrawer.vue` / `DiffWorkspace.vue`)  
> **核心标签**：守护进程管理 / 文件树 CRUD / 路径防逃逸 / 铁律 5 违规整改 / 暖炭黑色彩规范 / Monaco 快捷键捕获

---

## ① 知识点与问题背景 (Context & Problem Statement)

在第二次深度 Grill-Me 专项自省与交互代码审查过程中，暴露了数处影响开发效率、交互质感与架构严谨性的关键问题：

1. **守护进程生命周期脱缰与黑盒化**：
   在引入 `is_daemon: true` 后，AI Agent 能够启动诸如 `npm run dev` 或 `vite` 等长驻开发服务，但前端终端抽屉只展示常规命令行输出，缺乏对后台活动任务清单的查询与一键 `taskkill` 销毁机制，容易导致子进程遗留与端口冲突。
2. **文件资源管理器（File Explorer）功能单薄，缺乏必要的文件 CRUD 支撑**：
   用户只能在左侧文件树中单向点击既有文件，无法在工作区内直接新建文件（New File）、新建文件夹（New Folder）、重命名（Rename）或删除（Delete），迫使开发者频繁切回 Windows 资源管理器。
3. **违反铁律 5：存在浏览器原生 `window.prompt` 伪交互**：
   在 `LeftDrawer.vue` 中，修改会话标签直接调用了原生 `window.prompt()`，违反了项目级“严禁使用原生弹窗，所有弹窗必须采用 Warm Minimalist 居中模态框”的强制规范。同时，会话标题缺少原地重命名能力，且会话删除缺少居中二次确认，存在单手误触一键删除所有会话历史的潜在风险。
4. **代码编辑与 Diff 视图混淆及 Monaco 快捷键漏捕**：
   在左侧文件树中点击任意普通文件时，前端状态直接跳转至 `'diff'` 视图模式并提示“暂无代码差异对比”，而非直接打开 Monaco 单文件编辑器模式；在 Monaco 编辑器内部按下 `Ctrl+S` 时，由于 Monaco 编辑器实例内部原生截获了快捷键事件，未向外冒泡至全局 `window.addEventListener('keydown')`，导致直接热键保存落盘失效。
5. **视觉色彩脱节**：
   Monaco Editor 原生使用 `vs-dark` 预设主题，编辑区底色呈现偏冷黑（`#1e1e1e`），与项目级 Warm Minimalist 规范所确立的暖炭黑（`#1E1C1A`）存在视觉断层。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 1. 微内核插件架构与后台任务生命周期治理（铁律 7 约束）
按照架构铁律 7，任何工具执行必须通过 `registry.GetTool(name).Execute(ctx, args)` 调度，禁止在 `App` 结构体持有具体工具指针。
`terminal_tool` 在微内核中维护了一个全局线程安全的 `daemonTasks map[string]*DaemonTask`，每个任务记录了分配的 `task_id`、操作系统 PID、执行命令行、启动时间与终止控制通道。
通过扩展 `terminal_tool` 的入参契约：
- `action: "list"`：遍历返回活跃守护任务的序列化 JSON 数组；
- `action: "kill"` 配合 `task_id`：调用系统级进程树递归销毁与资源回收通道。
宿主 `app_shell.go` 仅需通过 `registry.GetTool("tool.terminal")` 派发执行，即可完全遵循单向无侵入的插件热插拔规范。

### 2. 沙箱边界约束与防路径穿越（Path Traversal Guards）
在向用户开放文件 CRUD 操作时，必须无条件防止路径穿越攻击（例如构造 `../../../../Windows/System32` 恶意逃逸）：
在 `internal/core/sandbox/fs.go` 中：
- `SafeCreateDir(path)`
- `SafeDelete(path)`
- `SafeRename(oldPath, newPath)`
底层均调用 `Resolve(path)`，使用 `filepath.Clean` 与 `filepath.Abs` 结合盘符大小写归一化（`normalizeDriveLetter`）和沙箱边界检查（`strings.HasPrefix`）。凡超出工作区目录的文件访问均坚决抛出 `SECURITY: Access Denied`，保障系统操作的沙箱安全隔离。

### 3. Monaco Editor 快捷键捕获机制与独立 Theme 注入
- **快捷键隔离机制**：Monaco Editor 运行在独立的 Canvas / Shadow DOM 事件循环中，其内部内置了 `KeyMod.CtrlCmd | KeyCode.KeyS` 的内置保存命令拦截器，默认阻止默认行为并阻断向外冒泡。因此，常规在 Vue 组件顶层挂载的 `window.addEventListener('keydown')` 无法捕获编辑器内部的 `Ctrl+S`。正确做法是使用 Monaco 原生 API：
  ```ts
  editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
    bench.saveEditor()
  })
  ```
- **暖炭黑主题定制**：
  在 `monacoEnv.ts` 中通过 `monaco.editor.defineTheme('tcode-warm-charcoal', ...)` 定义全局主题，显式指定：
  - `background: '#1E1C1A'`
  - `lineHighlightBackground: '#262320'`
  - `selectionBackground: '#D96B2733'`（陶土暖橙低饱和高亮）
  并在 `MonacoEditor.vue` 和 `MonacoDiffEditor.vue` 挂载时统一设置 `theme: 'tcode-warm-charcoal'`。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 步骤 1：后端插件契约与沙箱 CRUD 扩容

1. 在 `plugins/tool/terminal/terminal_tool.go` 中，新增 `action: "list"` 分支：
   ```go
   case "list":
       t.daemonMu.Lock()
       list := make([]map[string]interface{}, 0, len(t.daemonTasks))
       for _, dt := range t.daemonTasks {
           list = append(list, map[string]interface{}{
               "task_id":    dt.ID,
               "pid":        dt.PID,
               "command":    dt.Command,
               "start_time": dt.StartTime.Format(time.RFC3339),
               "status":     dt.Status,
           })
       }
       t.daemonMu.Unlock()
       raw, _ := json.Marshal(list)
       return string(raw), nil
   ```

2. 在 `app_shell.go` 中通过 Registry 统一调度：
   ```go
   func (a *App) ListDaemonTasks() (string, error) {
       tool, ok := a.registry.GetTool("tool.terminal")
       if !ok { return "[]", nil }
       rawArgs, _ := json.Marshal(map[string]interface{}{ "action": "list" })
       return tool.Execute(context.Background(), rawArgs)
   }
   
   func (a *App) KillDaemonTask(taskID string) (string, error) {
       tool, ok := a.registry.GetTool("tool.terminal")
       if !ok { return "", fmt.Errorf("tool.terminal not registered") }
       rawArgs, _ := json.Marshal(map[string]interface{}{ "action": "kill", "task_id": taskID })
       return tool.Execute(context.Background(), rawArgs)
   }
   ```

3. 在 `app_shell.go` 中封装 `CreateFile`, `CreateDirectory`, `DeletePath`, `RenamePath`，由 `a.sandbox` 强校验沙箱边界。

### 步骤 2：会话层原地重命名支撑与 TDD 验证
在 `internal/session/store.go` 中增加 `Rename(sessionID, newTitle string) error`：
- 去除空白字符；
- 检查会话是否存在；
- 更新内存中的 `SessionMetadata.Title` 并原子写入磁盘；
- 编写 `internal/session/store_rename_test.go` 确保单元测试 100% 通过。

### 步骤 3：消除原生弹窗，引入 Warm Minimalist 模态框与右键菜单
1. 在 `LeftDrawer.vue` 中消除全部 `window.prompt`：
   - 标签编辑改为 `s.openTagModal(sess)`；
   - 新增会话重命名按钮 `s.openRenameModal(sess)`；
   - 会话删除改为二次确认弹窗 `s.requestDeleteSession(sess.id)`；
   - 顶部工具栏增加新建文件 `＋📄` 与新建文件夹 `＋📁` 快捷按钮；
   - 文件树节点右键触发 `openFileContextMenu`，提供新建、重命名、删除、刷新与复制路径的快捷交互。
2. 弹窗完全遵循铁律 5：
   - 背景遮罩 `#000000/40` + `backdrop-blur-xs`；
   - 居中卡片背景采用暖米白（`#FAF8F5`）与陶土暖橙主操作按钮；
   - 必须支持键盘 `Esc` 快捷退出与显式 `[✕]` 关闭按钮。

### 步骤 4：Monaco 编辑器热键绑定与主题统一
在 `MonacoEditor.vue` 中增加：
```ts
editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
  bench.saveEditor()
})
```
并由 `monacoEnv.ts` 注册并分发 `tcode-warm-charcoal` 主题。

### 步骤 5：丰富 DiffWorkspace 与 TerminalDrawer 空状态及操作面板
- 在 `DiffWorkspace.vue` 空状态区，渲染暖色功能卡片（打开资源管理器、全局检索、新建空白文件）；
- 在 `TerminalDrawer.vue` 顶栏提供 `命令行控制台` 与 `⚡ 守护进程 (Daemons)` 切换标签页及任务表格，支持随时查看 PID 与一键强杀。

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **绝对禁止在业务层直接持有 Tool 实例**：
   切勿在 `App` 结构体或前端路由中绕过 Registry 直接实例化 `terminal.NewTool()`。所有后台任务的注册、查询与终止必须严格穿透 `a.registry.GetTool("tool.terminal").Execute()`，否则会导致守护任务映射表在不同实例间割裂，造成后台孤儿进程无法被查杀。
2. **Monaco 快捷键拦截陷阱**：
   在 Monaco 编辑器获得光标焦点时，浏览器的默认按键事件流被完全截断。切勿依赖外部父级 DOM 的 `@keydown` 监听器，必须使用 Monaco 实例的 `.addCommand()` 注入全局保存与快捷操作。
3. **文件树单击意图识别**：
   开发者在文件树单击普通代码文件的心理预期是“立即打开此文件查看或微调代码”，而非审查 Git 差异。因此在 `handleFileClick` 中，应当默认调用 `openEditorTab(node.path, 'edit')`，并在用户主动点击顶栏 `Diff` 按钮或从 Git 状态列表唤起时才激活 `diff` 对比视图。
4. **零原生交互铁律长效守卫**：
   严禁在任何 Vue 组件或脚本中引入 `window.confirm`、`window.prompt` 或 `window.alert`。所有阻断式或确认式交互均必须通过 Pinia Store 管理统一的 `pending...` 模态窗状态并在 UI 展现符合暖米白设计规范的模态框。
