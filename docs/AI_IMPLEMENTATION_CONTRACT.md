# 湉码 / tiancode — 后续 AI 施工合同（产品 + 架构）

> **读者**：后续接手的 AI 与人类开发者。  
> **目的**：把「原型有、现货没有或不真」的缺口做成可执行工作包，按包验收，禁止发挥成第二套产品。  
> **版本**：2026-09-12 · 对照发货栈 `Wails v2 + Go 微内核 + Vue 3`。  
> **视觉参考（只读）**：`archive/web_prototype.html`。  
> **发货 UI（只写）**：`frontend/src/**`。  
> **发货内核（只写）**：`app.go` / `app_*.go` / `internal/**` / `plugins/**`。

本文是后续施工的**唯一任务合同**。旧 PRD、`docs/knowledge/*`、`docs/technical_reviews/*`、Tauri/Python 计划**不是**施工依据。冲突时以本文 + `AGENTS.md` 铁律为准。

---

## 0. 开工前必读（不读不准改）

1. `AGENTS.md` 铁律 0 / 0.5 / 0.8：零 Demo、fail-closed、活路径只有 Wails+Go+Vue。  
2. `docs/ARCHITECTURE.md`：单一 `ExecutionEngine.executeDirectLLM`，禁止再开第二条 LLM 调度。  
3. `docs/V1_FEATURE_BOUNDARY_MATRIX.md`：现状评分以用户能靠为准，禁止把半成品改回 🟢。  
4. 编译：`frontend` 用 `npm.cmd run build`；Go 用  
   `go build -tags "desktop,production" -ldflags="-H windowsgui -s -w" -o bin/tiancode.exe .`  
   测试：`go test ./internal/... ./plugins/...`。  
5. **每完成一个工作包**：对应 `*_test.go` 变绿 + `npm.cmd run build` 通过，再 `git push`。未验证不得声称完成。

---

## 1. 产品目标（给用户用，不是给文档打勾）

用户打开 湉码应当能完成这一条主旅程，中间不猜、不假成功：

```
打开真实文件夹 → 配置真渠道并探活 → 选策略（默认只读）
→ 提问 → Agent 先检索/地图再改（或只读回答）
→ 人能看见改了哪些文件 → Diff 处理 → 可选跑测试 → git commit
```

**成功标准（产品）**：一个不熟悉本仓库的人，按 README 安装后，用自己的 API Key，在本仓库上完成「审查一处 Go 文件并给 Diff」而不被假入口误导。

**非目标（禁止施工）**：Swarm 多智能体、OAuth/Sub2/Cap 认证矩阵、MCP SSE、Air-Gap、技能市场、用量账单、知识图谱当主功能、把 `archive/` 接回主路径、全量 LSP、PTY 多终端。

---

## 2. 架构铁律（违反即打回）

| ID | 铁律 |
|----|------|
| A1 | 聊天主循环只改 `internal/core/loop`。`App.SendMessage` 只组上下文、转发事件、落盘 Task。 |
| A2 | 工具只经 `host.Registry`。禁止在 `SendMessage` 里 `switch toolName`。新增工具只需在 `registerWorkspaceTools`(app.go) 注册一处，`NewApp` 与 `SetWorkspace` 共用该入口（`RegisterOrReplace` 幂等）。 |
| A3 | `SetWorkspace` 必须重绑全部工程算子（fs/git/terminal/search/arch 经 `registerWorkspaceTools`），并经由 `configureEngine` 重新注入绑定新工作区的 verify(MCP/TDD) 回调。漏绑 search 视为回归。 |
| A4 | 无渠道、无 Key、无 endpoint → 拒绝发网，禁止填 OpenAI 默认 URL、禁止写死模型名。 |
| A5 | 禁止假数据：假会话、假 MCP、假在线、假 PASS。空状态必须空。 |
| A6 | 禁止 `alert`/`confirm`/`prompt`。弹窗：居中、Esc、遮罩、显式 X。 |
| A7 | 原型 HTML 只抄交互契约，禁止把 Demo 文案/假渠道/假 4 条会话拷进 Vue。 |
| A8 | 不新增活动栏图标。实验能力只进设置「实验特性」。 |
| A9 | 文档与代码同步：改行为必须改本文对应工作包状态，禁止只改矩阵把 🟡 刷成 🟢。 |
| A10 | Windows 交付：改完功能按 `scripts/build-windows.ps1` 或既有安装器流程出包；不提交巨大二进制到 git。 |
| A11 | 依赖方向严格单向：`app`(main 组合根) → `internal/core`(loop/memory/host) → `pkg/plugin/v1` / `internal/llm` / `internal/session`。core 包之间不得反向依赖；`loop` 保持无状态，不引入 `session`/`memory`/`agent` 依赖——多轮记忆装配由 `app_chat.go` 调 `internal/core/memory` 后注入 `EngineRequest.Messages`。 |
| A12 | 术语消歧：「host」两义——`internal/host` 是**核心层插件注册契约**（非应用宿主），`app.go`/main 才是**应用宿主（组合根）**。二者依赖为 app → host，host 不反向依赖 app。 |

---

## 3. 现货基线（不要重做）

已经闭环、后续 AI **默认不要重写**，只修回归：

- 单一 `executeDirectLLM`；TaskModel；人话收尾；「继续」接 Goal。  
- SafetyRail：路径沙箱、高危命令、密钥剥离。  
- 策略 `analyze` / `implement` / `tdd`：analyze 拦写盘与第 1 轮非清单 read。  
- `search_workspace` 插件；换仓重绑。  
- 文件树懒加载；编辑器页签缓冲；pending Diff 挡发送。  
- Git：status / branch / stash / stage / commit / pull / push / hunk apply。  
- 渠道 DPAPI、探活、`FetchUpstreamModels`。  
- MCP stdio 启停探活；技能/规则落盘并注入 prompt。  
- 活动栏：对话、文件、Git、终端、设置（图谱/用量已不在主栏）。

---

## 4. 原型缺口 → 工作包（按序，禁止跳号抢做 P2）

每个包含：**用户故事、改哪些文件、测试、完成定义**。未满足「完成定义」不得勾完成。

---

### WP-1 诚实命名与去掉误导灯（P0，先做，半日级）

**问题**：控件名像完整产品，实际不是。用户当摆设。

**必须改**

| 现文案 | 改为 |
|--------|------|
| Git「快照」 | 「Stash（储藏）」 |
| 渠道未测速显示 online/极佳 | 仅探活成功后才 `online`；默认 `standby` / `未测速` |
| `auth_type` 展示成 OAuth 等 | 现货只支持 bearer，UI 显示「Bearer Token」或不显示未实现类型 |
| Ctrl+K 文案「检索分支、文件与算子」 | 「跳转：设置 / 会话 / 已打开文件」 |
| 顶栏「就绪」绿灯 | 未探活主渠道时不要用成功绿；用「未探活」灰或琥珀 |

**文件**：`frontend/src/components/LeftDrawer.vue`、`ChatCockpit.vue`、`App.vue`、`stores/workbench.ts`（`saveChannelAction` 的 `status`）、渠道列表模板。

**完成定义**

- [x] 未点「测速」的渠道卡片不出现「在线/极佳」。  
- [x] 全文搜索 UI 无「OAuth」「快照时间旅行」暗示。  
- [x] 无 Go 行为变化；`go test ./internal/config` 绿。

---

### WP-2 人可全仓搜文件与内容（P0）

**问题**：过滤框只搜已展开节点；Ctrl+K 同样。原型是「人能找到文件」。

**必须实现**

1. 前端搜索框走 Go：`search_workspace` 的 `find`（文件名）与 `grep`（内容），**不**依赖已加载树。  
2. 结果列表：路径 + 行号；点击 `openEditorTab`。  
3. 空结果、错误要显示真实错误，禁止假「无文件」。  
4. 仍跳过 `node_modules` / `vendor` / `.git`（search 插件已有）。

**建议 IPC**：已有 Registry 工具可在 `App` 增加薄封装，例如 `SearchWorkspace(action, query, path string) (string, error)` 调 `registry.GetToolByName("search_workspace")`，不要复制一份 Walk。

**文件**：`app_config.go` 或 `app_shell.go`、`wailsBridge.ts`、`DiffWorkspace.vue`、`workbench.ts`。  
**测试**：`plugins/tool/search/search_tool_test.go` 已有 grep；补 `App` 级或 store 不测也可，但必须有 Go 测试覆盖「query 命中临时文件」。

**完成定义**

- [x] 文件树未展开 `internal/core/loop` 时，搜索 `llm_path.go` 能出结果并打开。  
- [x] grep `DenyByStrategy` 能返回路径:行号。  
- [x] 换工作区后搜索根是新目录（WP 回归 A3）。

---

### WP-3 Agent 先检索再深读（P0，内核）

**问题**：有 search 工具，模型可以不用；implement 开局可盲扫。

**必须实现（硬闸，不是再加提示词）**

1. `analyze`：**第 1 轮**只允许 `search_workspace`、`fs_control` 的 `list`、以及现有清单白名单 `read`。其它 read/exec 继续拦。  
2. `implement`：**第 1 轮**若未调用 `search_workspace` 或根 `list`，禁止 `fs_control` 读深层路径（与 analyze 同一 `isAllowedAnalyzeFirstTurnFile` 或共享函数）。第 2 轮起放开。  
3. 拦截原因必须写进人话：`[策略拦截] 请先 search_workspace 或 list 工作区根`。  
4. **不要**把 `maxLLMTurns` 再加大当「更自主」。20～24 足够；触顶必须人话+汇总（已有）。

**文件**：`internal/core/loop/strategy.go`、`strategy_test.go`、`tools.go`（已传 `turn`）。

**完成定义**

- [x] `TestDenyByStrategy_ImplementTurn1DeepReadDenied`：turn=1、`path=internal/foo.go` → deny。  
- [x] turn=1、`search_workspace` → allow。  
- [x] turn=2、深层 read → allow。  
- [x] `go test ./internal/core/loop` 绿。

---

### WP-4 写盘对用户可见（P0）

**问题**：原型是改完能审；现货是先写盘再横幅。完整「写前沙箱」太大，本包做**最小诚实闭环**。

**必须实现**

1. Agent `EventFilesChanged`：自动 `openFileDiff(path, 'diff')` + 打开右侧栏（已有部分则补齐「必定切到该文件 Diff」）。  
2. 横幅列出全部 `pendingDiffFiles`，每项可点。  
3. 「全部放弃」调用已有 revert；「全部采纳」对列表 `GitStage`。禁止 toast 假装成功。  
4. 发送拦截弹窗保留。  
5. 文案改为「已写入工作区，请审查 Diff」，禁止「预览/未落地」。

**文件**：`workbench.ts` `onToolEnd`/`files_changed`、`ChatCockpit.vue` 横幅、`DiffWorkspace.vue`。

**完成定义**

- [x] 一次对话写 2 个文件，右侧自动打开其中一个 Diff，横幅显示 2 个路径。  
- [x] 全部放弃后横幅清空、任务态不再 `pending_diff`。  
- [x] 无新假按钮。

---

### WP-5 策略选择可理解（P0 交互）

**问题**：输入框药丸单击轮询三态，易误触可写。

**必须实现**

- 发送前策略选择保持弹窗三选一（已有 `isStrategyPickerOpen`）。  
- 输入条药丸改为**展示 + 点击打开同一弹窗**，禁止单击轮询。  
- 默认 `analyze`；「跳过」= 保持 analyze（已做，回归锁定）。  
- 弹窗写明：只读拦写盘；TDD 写后跑测试；直接改代码会写磁盘。

**完成定义**

- [x] 单击药丸只开弹窗，不改变策略直到点确定。  
- [x] `skipStrategyChoice` 测试或手工：策略仍为 analyze。

---

### WP-6 技能 / 规则按原型可用（P1）

**必须实现**

1. 导入本地 `SKILL.md`（选文件对话框 → 解析可选 YAML frontmatter 的 name/description + 正文为 prompt → `SaveSkill`）。无 frontmatter 则文件名当 name、全文当 prompt。  
2. `@` 菜单：启用技能 + **search find 当前 query** 的文件（最多 20），插入 `@path` 并在发送时 `ReadFile` 把内容附进用户消息（注意体积截断，例如 8KB/文件）。现在只插字符串路径视为未完成。  
3. `/` 菜单：保留 `/tdd` `/diff` `/term`；**删除或移入实验** `/audit`。MCP 名 `/xxx` 保持探活。

**文件**：`app_config.go` `ImportSkillMarkdown`、`workbench.ts` mention、`app_chat.go` 发送前附件展开。

**测试**：临时 SKILL.md → ListSkills 含其 prompt。

**完成定义**

- [x] 导入真实 SKILL.md 后新对话 system 含该 prompt（技能启用时）。  
- [x] `@` 选中 `README.md` 后模型能读到文件内容，不是只有路径字。

---

### WP-7 MCP 按「能用的 stdio」做完（P1）

**必须实现**

- 表单：command、args、**env 多行 KEY=VALUE**（结构体已有 `Env`）。  
- 探活失败展示 `MCPTestResult.error` 全文，禁止只 toast「失败」。  
- 不实现 SSE。类型选择器不要出现 SSE，或选 SSE 时明确「未实现」且禁止保存为已启用。

**完成定义**

- [x] 保存带 env 的 MCP，json 文件里看得到 env。  
- [x] 错误命令探活，UI 显示真实 stderr/错误字符串。

---

### WP-8 TDD 结果给人看（P1）

**必须实现**

- `RunTDDValidation` 结果在对话里插入一条 **系统可见卡片**（或 assistant 工具输出保留全文，前端折叠：「Go：pass/fail；Npm：跳过/因无 scripts.test」）。  
- 本仓 frontend 无 test 脚本时，UI 写「未配置 npm test，已跳过」，不要假装跑过前端。  
- 超时 60s：人话「测试超时已中断」，状态 `tdd_failed`。  
- **不要**为了绿而给 frontend 编假 test 脚本。

**完成定义**

- [x] `/tdd` 在本仓库：能看到 go test 输出摘要；明确 npm 跳过原因。  
- [x] 失败时顶栏「测试未通过」。

---

### WP-9 对话卡片按原型（P1，薄）

**必须实现**

- [x] 思考块默认折叠，点击展开（现货常整段展开）。  
- [x] 多工具默认折叠为「调用 N 个工具」，点击展开入参/输出。  
- [x] 复制 / 重新生成保持接真会话（已有则补「折叠」即可）。

**禁止**：点赞点踩若无落盘就不要画。

---

### WP-10 Esc 分层关闭（P1）

优先级：mention/slash 浮层 → 二级弹窗（渠道/MCP/技能/pending Diff/策略）→ 设置/图谱 → 无操作。  
与 `AGENTS.md`、原型 PRD 2.5 节一致。

**完成定义**：
- [x] 设置打开时 Esc 关设置；设置里渠道子层先关子层。

---

## 5. 明确不要做（写进 PR 描述也要写「未做」）

- 加大 `maxLLMTurns` 冒充更强 Agent。  
- 知识图谱力导向、时间轴、手动加节点。  
- Token 大盘当账单。  
- `/audit` 当安全产品。  
- OAuth 登录、故障转移、SSE MCP。  
- Swarm UI、`budget()/parallel()`。  
- 把 `archive/web_prototype.html` 的 Demo 会话拷进 Vue。  
- 新增活动栏按钮。  
- 重写 `workbench.ts` 为多个 store（除非单独开包且旧入口全部删除）。  
- 引入 Tauri/React/Python。

---

## 6. 建议实施顺序与并行

```
WP-1（命名/绿灯） ──► WP-2（人搜索） ──► WP-3（Agent 硬闸）
                         │
                         ├──► WP-4（Diff 可见）
                         └──► WP-5（策略弹窗）
之后：WP-6 技能@文件 → WP-7 MCP → WP-8 TDD UI → WP-9 折叠 → WP-10 Esc
```

WP-1 可与任何包并行。WP-3 依赖 search 已注册（已满足）。WP-4 不要做成完整写前隔离。

---

## 7. 每包验证命令（Windows）

```powershell
cd C:\Users\13605\tiancode
# Go
& E:\pro\tools\go\bin\go.exe test ./internal/core/loop ./internal/config ./plugins/tool/search ./internal/agent ./plugins/rail/safety
# 前端
cd frontend; npm.cmd run build; cd ..
# 桌面（改 IPC 后）
& E:\pro\tools\go\bin\go.exe build -tags "desktop,production" -ldflags="-H windowsgui -s -w" -o bin/tiancode.exe .
```

声称「用户可用」之前：安装包或直接跑 `bin/tiancode.exe`，用**空渠道**确认 fail-closed，再用真 Key 走一遍主旅程。

---

## 8. 完成后的 git 说明模板

```
feat(wp-N): <用户可感知的一句话>

- 改了：...
- 验证：go test <包> ; npm run build
- 未做：本文第 5 节所列
```

禁止在 commit 里写「v1 全部达标」「对标 Cursor」。

---

## 9. 给后续 AI 的自我检查（交卷前）

- [x] 是否改了 `archive/` 或把 HTML 原型当发货？若是，打回。  
- [x] 是否新增假在线/假会话？若是，打回。  
- [x] `SetWorkspace` 是否仍注册 search？  
- [x] 用户能否不读源码完成「搜文件 → 打开 → 只读提问」？  
- [x] 矩阵/README 有没有把未完成项标成完成？  

不通过检查不得 `git push` 并宣布工作包完成。
