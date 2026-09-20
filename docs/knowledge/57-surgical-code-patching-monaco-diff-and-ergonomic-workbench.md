# 57. 手术级精准局部补丁算子、Monaco 原生 Diff 审查与全域人机工程学工作台演进

> 记录并深度剖析从“100% 全量文件覆写”向“局部精准替换算子”的跨越，以及手写 HTML Diff 向原生 Monaco Diff Editor 的重构，并沉淀终端守护任务、中文无空格 @ 引用、Token 安全上下文裁剪与无级拖拽分栏的人机工程学体系。

---

## ① 知识点与问题背景 (Context & Problem Statement)

在湉码 (tiancode) 持续演进过程中，经过全面的深度穿透式审查 (Grill-Me Audit)，暴露出一组严重制约开发体验与系统稳定性的关键瓶颈：
1. **100% 全量覆写陷阱 (Full Overwrite Trap)**：
   原有的 `fs_control` 仅有 `read` 与 `write` 动作。当大模型试图修改一个 1,500 行文件里的第 20 行时，必须耗费数千输出 Token 将整份代码全量输出。大模型极易偷懒输出 `// ... rest of code unchanged ...`，造成用户源文件被直接截断损毁。
2. **终端执行缺乏守护任务支持 (Terminal Daemon Locking)**：
   原有的 `exec_command` 是纯阻塞式同步执行（固定 60 秒硬超时）。大模型尝试执行 `npm run dev`、`vite`、`go run .` 等长耗时常驻任务时，程序直接挂起死锁 60 秒并超时报错。
3. **中文与紧凑输入下 `@` 文件引用失效**：
   `expandMentionedFiles` 使用 `strings.Fields(prompt)` 分词，当用户输入诸如“`请优化@main.go中的逻辑`”时，由于前后无空格，无法识别到 `@main.go`。
4. **会话裁剪腰斩引发 API 400 崩溃**：
   `buildConversationWindow` 机械截断早期消息时，如果切片点卡在 `assistant`（包含 `tool_calls`）与其返回的 `tool` 之间，会导致首条消息为孤立的 Tool 响应，Anthropic/OpenAI 接口直接抛出 `400 Bad Request`。
5. **Diff 模式为伪 Monaco Editor (纯 HTML `<div>` 拼接)**：
   `DiffWorkspace.vue` 的 Diff 审查未调用 Monaco 的 Diff 引擎，代码无语法高亮、不支持并排与内联对比切换、未改动区域无法智能折叠。
6. **聊天区 Markdown 代码块无语法高亮与一键采纳**：
   大模型输出的代码块无语言语法着色，且缺乏快捷「复制代码」与「应用至编辑器」交互。
7. **左侧抽屉与底部终端缺乏平滑无级拖拽手柄**：
   左栏固定 270px 宽度，长文件名严重截断；终端高度无法随心调节。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 1. 手术级局部替换算子 (`fs_control(replace)`)
- **设计哲学**：对齐现代 Coding Agent 标准（如 Cursor / Claude Code），采用原子级字符串与特征区间定向替换。
- **参数契约**：
  - `path`: 目标文件路径；
  - `target_content` (别名 `old_string`): 需匹配的目标代码块；
  - `replacement_content` (别名 `new_string`): 替换后的新内容；
  - `start_line` / `end_line` (可选): 限制搜索行号范围，支持局部多处相似代码时的定向消除歧义；
  - `allow_multiple` (布尔，默认 `false`): 当有多处完全相同时阻断执行，防止误伤。
- **安全保障**：替换前自动触发 `snapshotMgr.CreateSnapshot` 建立影子快照，并采用临时文件原子写入 (`AtomicWriteFile`)。

### 2. 终端守护任务状态机 (Daemon Process Management)
- **非阻塞脱机设计**：对于长期运行命令，注入 `bgCtx, bgCancel := context.WithCancel(context.Background())`，创建非阻塞子进程并以线程安全 `sync.Map` 进行生命周期托管。
- **多状态流转**：
  - `action: "run", is_daemon: true`：立即返回任务唯一 `task_id`、操作系统 `PID` 与运行中状态；
  - `action: "status"`：查询任务执行耗时、是否存活及尾部日志缓冲；
  - `action: "kill"`：在 Windows 上通过 `taskkill /F /T /PID` 递归击杀整棵进程树，杜绝孤儿子进程残留。

### 3. 上下文安全窗口与 User 角色对齐准则 (Token-Safe Windowing)
- **协议约束**：主流 LLM 服务商（OpenAI, Anthropic, Gemini, DeepSeek）的 Chat Completions 规范要求：在 `system` 消息之后，对话的第一个 Message 必须是 `user` 角色，且工具调用轮次必须严格包含 `assistant(tool_calls) -> tool(result)` 闭环。
- **安全对齐算法**：
  ```go
  // 裁剪后确保 startIndex 始终步进到 user 角色消息，绝不把孤立的 assistant 或 tool 消息暴露在开头
  for startIndex < len(processed)-1 && processed[startIndex].Role != "user" {
      startIndex++
  }
  ```

### 4. 原生高保真 Monaco Diff Editor 架构
- **Monaco 双模型架构**：
  通过 `monaco.editor.createDiffEditor` 构建实例，并为原文件与修改后文件分别绑定 `monaco.editor.createModel(text, language)`；
- **双模实时切换**：通过 `renderSideBySide` 选项，支持在“双栏并排 (Side-by-Side)”与“单栏内联 (Inline)”对比模式间无缝切换；
- **Hunk 工具条共存**：保留 Git Hunk 分块的 `[✓ 采纳块]` 与 `[✕ 丢弃块]` 动作带，实现“专业差异渲染”与“敏捷版本控制”的有机融合。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 1. 后端局部补丁与守护任务调用示例
- **局部代码替换**：
  ```json
  {
    "action": "replace",
    "path": "pkg/server/server.go",
    "target_content": "func Run() {\n\tprintln(\"old\")\n}",
    "replacement_content": "func Run() {\n\tprintln(\"new\")\n}",
    "allow_multiple": false
  }
  ```
- **启动后台开发服务器**：
  ```json
  {
    "action": "run",
    "command": "npm run dev",
    "is_daemon": true
  }
  ```
- **查询与终止后台任务**：
  ```json
  { "action": "status", "task_id": "task-823910" }
  { "action": "kill", "task_id": "task-823910" }
  ```

### 2. 前端 Markdown 语法高亮与一键代码采纳
- 在 `frontend/src/core/markdown.ts` 中通过 `marked.use({ renderer: { code(...) } })` 与 `highlight.js` 实现自动语法着色；
- 生成代码卡片附带统一头部与 `data-code` 属性；
- 在 `ChatCockpit.vue` 中挂载事件委托，点击 `[📋 复制]` 写入剪贴板，点击 `[⚡ 应用至编辑器]` 自动将代码注入当前活动 Monaco 编辑器并切入双栏视口。

### 3. 全局拖拽手柄与尺寸记忆
- **左侧抽屉 (LeftDrawer)**：右侧边框内嵌垂直拖拽手柄，宽度范围 200px ~ 550px，持久化至 `localStorage('tiancode_left_drawer_width')`，支持双击一键复位至 270px；
- **底部终端 (TerminalDrawer)**：顶部边框内嵌水平拖拽手柄，高度范围 140px ~ 75vh，持久化至 `localStorage('tiancode_terminal_height')`，支持双击一键复位至 240px。

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **避免模糊匹配多处误伤**：在调用 `fs_control(replace)` 时，尽可能携带前置或后置 1~2 行上下文，确保 `target_content` 在目标作用域内具备唯一性；若确定批量替换需显式传递 `allow_multiple: true`。
2. **Windows 进程彻底杀灭**：后台常驻任务直接使用 `cmd.Process.Kill()` 在 Windows 上往往只能杀死顶层 cmd.exe，其子进程（如 node.exe、go.exe）仍会滞留为孤儿进程。必须统一调用 `taskkill /F /T /PID` 确保整棵进程树干净退出。
3. **保持大模型前缀缓存 (Prefix KV Cache)**：会话窗口裁剪应优先采用原位输出修剪 (`pruneHistoricalOutput`)，仅在超出极大安全阈值 (120,000 字符) 时才从头部步进裁剪，且必须保证步进起点为 `user` 角色。
