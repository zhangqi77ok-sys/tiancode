# M6 编程智能体可用性

日期：2026-09-27  
状态：待实现  
范围：跨轮工具历史、工作区探索工具、对话区可读性  
非目标：文件树 / 编辑器 / git 面板 / git 写操作 / 语法高亮库 / .gitignore 解析 / 会话自动标题 / 四环人工验收

> 本里程碑让第一版四环在**连续多轮写代码**时真正可用：模型能看见自己改过什么，能列出目录和搜索内容，用户能读懂回复和工具结果。

## 1. 背景与问题

当前 `main`（`ba958aa`）四环代码与契约测试已齐，但有三处挡住「连续写代码」：

1. **跨轮失忆。** `agent.deriveMessages` 只重放 `user_message` / `assistant_message`。同一轮内工具回填是对的（`TestAgent_*` 覆盖），下一轮 `Run` 重新 derive 时，上一轮的 `tool_call` / `tool_result` 全部丢掉。模型看不到改了哪些文件。
2. **账本缺调用 ID。** `EventToolCall` 只存 `{name, arguments}`，`EventToolResult` 只存 `{name, content, is_error}`。OpenAI 兼容协议要求 `assistant.tool_calls[].id` 与 `role=tool` 的 `tool_call_id` 配对。不补 ID 无法合法回放。
3. **探索能力缺口。** `fs` 只有 read/write/replace；没有目录列举。内容搜索只能靠 `shell`，无界、无忽略、不可测。
4. **对话不可读。** 助手消息纯文本；`thinking` 已进事件桥和账本 delta，UI 丢弃；工具卡只有 200 字摘要；`Replay` 不含工具，切换会话后工具卡消失。

## 2. 目标与验收

做完后，下列全部为真：

- 第二轮 `Send` 发给模型的 `Messages` 含上一轮完整的 `assistant(tool_calls)` + `role=tool`，ID 配对正确；单条工具结果超过 4096 字节时模型侧截断并标注，账本仍是全文。
- 模型可调用 `fs` 的 `list`（非递归、有界）和独立工具 `search`（工作区内容搜索、有界、跳过内置忽略目录）。
- 对话区：助手消息渲染 markdown；有 thinking 时流式展开、终态折叠；工具卡可展开看全文；刷新/切换会话后工具卡仍在。
- 新契约测试先红后绿；既有 `go test ./...` 与 `frontend` vitest 不回退。
- 文档与代码同一批提交：`CONTRACTS` / `ARCHITECTURE` / `MILESTONES` / `TESTING` / `PENDING` + ADR-0007。
- `VERSION` 升为 `0.2.0`；开发完成后按 `STANDARDS` §5 跑 `scripts/release.ps1`。

## 3. 架构

不新增分层、不新增模式。变化点全部落在已有位置：

| 变化 | 位置 | 原因 |
| --- | --- | --- |
| 回放算法 | `internal/core/agent` 的 `deriveMessages` | 模型上下文是 agent 的职责；账本仍是事实源 |
| 调用 ID 落盘 | `agent.turn` 写 `EventToolCall` / `EventToolResult` 时带 `id` | Kind 字符串不改（C-SES：改名 = 旧账本不可重放）；只加 JSON 字段 |
| 截断策略 | derive 时截断，**不改账本** | 审计/导出/UI 展开要全文；截断是发给模型的视图 |
| `list` | `internal/platform/fstool` 新 action | 与 read/write/replace 同属文件、同受 C-FS-4 路径校验 |
| `search` | 新包 `internal/platform/searchtool` | 内容搜索不是文件读写；独立契约、独立超时 |
| 装配 | `internal/app.newRegistry` | 启动与切换工作区必须走同一装配（C-WS-2） |
| UI 投影 | `ChatService.Replay` 的 `ChatMessage` | 界面与导出同源（C-SES-10） |
| 渲染 | `frontend/src`：markdown helper + `App.vue` / `chat` store | 壳层只做展示，不改契约 |

数据流（相对 M2 只加粗了回放与投影）：

```
用户输入 → ChatService.Send
  → ledger.Append(user_message)
  → deriveMessages(ledger)          ← 本轮改：含 tool_calls + tool
  → agent.Loop / ChatRuntime / tools
  → 账本：delta / tool_call(id) / tool_result(id) / assistant_message
  → 事件桥：chunk(delta, thinking) / tool(name, status, summary, content) / terminal
  → UI：markdown + thinking + 可展开工具卡
切换会话 → Replay(ledger)            ← 本轮改：投影 tool 卡与 thinking
```

## 4. 跨轮工具历史

### 4.1 账本字段（向后兼容）

Kind 不改。新写入：

- `tool_call`：`{id, name, arguments}`
- `tool_result`：`{id, name, content, is_error}`

`id` 取模型返回的 `ToolCall.ID`；若上游给空 ID，写入方生成 `call-{seq}`（`seq` 为即将写入的该 `tool_call` 事件序号）。`tool_result` 必须抄同一 `id`。

旧账本缺 `id`：回放时为每条 `tool_call` 合成 `call-{seq}`；`tool_result` 按 **§4.3 配对规则** 挂到未配对的 call 上。

### 4.2 deriveMessages 算法

一次正向扫描账本，产出 `[]llm.Message`。只使用这些 Kind：`user_message`、`tool_call`、`tool_result`、`assistant_message`。`assistant_delta` 不进入模型上下文（过程量；最终文本以 `assistant_message` 为准）。`turn_end` / `session_renamed` / `error` 忽略。

扫描状态：

- `pendingCalls []ToolCall`：当前步骤已见、尚未冲刷的调用
- `pendingResults []Message`：已配对的 `role=tool` 消息（Content 已按 §4.4 截断）

`assistant_delta` **不进入模型上下文**（过程量；最终文本只来自 `assistant_message`）。带 `tool_calls` 的 assistant 消息 `Content` 固定为空串——中间自语若再拼 delta 会与锚点双计。

冲刷（`flushToolStep`）：若 `pendingCalls` 非空 **且** 每条 call 都有对应 result，则追加：

1. `assistant{Content: "", ToolCalls: pendingCalls}`
2. 各 `role=tool` 消息（顺序与 calls 一致）

然后清空 pending。若存在 **没有 result 的 call**（崩溃发生在执行中）：**整步丢弃，不发给模型**。账本仍保留这些事件。禁止发出「有 tool_calls、缺 tool 消息」的非法协议。

事件处理：

| 事件 | 动作 |
| --- | --- |
| `user_message` | 先 `flushToolStep`；再追加 `user` |
| `tool_call` | 若 `pendingResults` 已与当前 `pendingCalls` 数量相等（上一步已齐），先 flush；再把该 call 加入 pending（id 按 §4.1） |
| `tool_result` | 按 §4.3 配到某条 pending call，写入对应 `role=tool` |
| `assistant_message` | 先 `flushToolStep`；再追加 `assistant{Content: text}` |
| 扫描结束 | `flushToolStep`（处理「有工具无最终锚点」的已完成步骤；半截步骤因缺 result 被丢弃） |

同一模型步内的并行调用在账本中的顺序是：N 条 `tool_call` 随后 N 条 `tool_result`。上表在「calls 未齐、results 为空」时不会提前 flush，因此并行步会合成 **一条** 带 N 个 `ToolCalls` 的 assistant 消息。

### 4.3 配对规则

1. `tool_result.id` 非空：挂到 `pendingCalls` 中 `ID` 相同的那条。找不到则挂到最早仍未配对的 call。
2. `tool_result.id` 为空（旧账本）：优先挂到最早未配对且 `name` 相同的 call；否则最早未配对的 call。
3. 配上后，该 `role=tool` 的 `ToolCallID` 等于被配对 call 的 `ID`。

### 4.4 截断（发给模型的视图）

常量 `toolResultModelLimit = 4096`（字节，UTF-8）。

- `len(content) <= 4096`：原样。
- 否则：`content[:4096]`（若切在多字节字符中间，向前回退到完整 rune 边界）+ `"\n\n[truncated, original N bytes]"`。`N` 为截断前字节数。
- 账本 `content` 不改。UI 展开用账本全文（UI IPC 另有 64KiB 上限，见 §6）。

截断发生在 derive 时，这样旧会话重放也享受同一预算，不必改写历史文件。

## 5. 探索工具

### 5.1 `fs` action `list`

`Schema.action` 枚举增加 `"list"`。新参数：无（沿用 `path`）。

行为：

- `path` 缺省为 `"."`。必须通过现有路径校验（C-FS-4）；越界 → `IsError`，文件零修改。
- 目标必须是目录；不存在或不是目录 → `IsError`，可读原因。
- **非递归**：只列该目录下一层。
- 输出有界：最多 500 条；超出则先输出 500 条，再追加一行 `(truncated, showing 500 of N entries)`。`N` 为实际条目数。
- 不含 `.` / `..`。隐藏文件（以 `.` 开头，含 `.git` 目录项）**包含**。非递归，不会走进任何子目录。
- 排序：目录优先，然后按名称字节序。
- 格式（一行一条，稳定，给模型读）：

```
dir  cmd/
dir  docs/
file go.mod  1234
file README.md  5678
```

`file` 第三列为字节大小。无法 `Stat` 的条目仍输出名称，大小为 `-`。
- 空目录：成功，`Content` 为 `empty directory`。
- 超时：沿用 `fstool` 现有 30s（C-TOOL-1）。

### 5.2 独立工具 `search`

新类型 `searchtool.Tool`，`Name() == "search"`。

描述（给模型）：在工作区内搜索文件内容。返回 `path:line:text`。默认跳过 `.git` / `node_modules` / `vendor` / `dist` / `bin`。

Schema：

| 字段 | 约束 |
| --- | --- |
| `pattern` | 必填。Go `regexp` 语法 |
| `path` | 可选，相对工作区，默认 `"."`。受 C-FS-4 同等越界校验 |
| `glob` | 可选。只匹配**文件名**（`filepath.Match`，如 `*.go`）。非法 glob → `IsError` |
| `max_matches` | 可选。默认 50，上限 200；小于 1 当 50 |

行为：

- 从 `path` 起递归走文件。目录名（任意一层）属于忽略名单则跳过整个子树。若用户把 `path` **显式**设为忽略名（例如 `vendor`），则 **仍搜索该根**，只跳过其内部再出现的忽略名。
- 忽略名单（精确目录名，大小写按 Windows 不敏感比较）：`.git`、`node_modules`、`vendor`、`dist`、`bin`。
- 跳过二进制：文件头 8KiB 含 NUL。
- 跳过单个文件大于 1MiB。
- 无效正则 → `IsError`，原因含 `invalid pattern`。
- 零匹配 → **成功**（`IsError=false`），`Content` 为 `no matches`。避免模型把「没找到」当成工具坏了而空转。
- 输出行：`相对路径:行号:该行文本`（行文本去掉尾部 `\r`）。总输出上限 64KiB；超过则截断并追加 `(truncated, output limit 64KiB)`。
- 达到 `max_matches` 后停止扫描，若还有未扫文件则追加 `(truncated, max_matches M)`。
- 超时 15s。超时与取消都走现有 `tools.ToolResult.TimedOut`（不新增端口字段），`Content` 携带已捕获输出（C-TOOL-2）。超时或取消且零匹配：`TimedOut=true`，`Content` 为 `TIMEOUT` 或 `cancelled`（保证非空，满足 ToolResult「Content 永远非空」）。超时有命中：`IsError=false`，`TimedOut=true`，正文后追加 `TIMEOUT` 标记。

### 5.3 装配

`newRegistry` 在 fs/shell/git 之后 `Register(searchtool.New(workDir))`。切换工作区仍走 `newRegistry`（C-WS-2）。

本轮 **不** 给 `shell` 加额外拦截；search 存在后系统提示不必改（无独立系统提示模块）。工具描述本身须写清何时用 `search` 而不是 `shell rg`。

## 6. 对话区

### 6.1 Replay 投影

`ChatMessage` 扩展（JSON 字段）：

| 字段 | 用途 |
| --- | --- |
| `role` | `user` / `assistant` / `tool` |
| `content` | 正文；tool 为完整结果（UI 层再截断芯片） |
| `toolName` | role=tool |
| `status` | `success` / `error`（来自 `is_error`） |
| `thinking` | role=assistant；该锚点之前、上一锚点之后的 delta `thinking` 拼接 |

`Replay` 扫描：

- `user_message` → user
- `tool_result` → tool 卡（name/status/content）；孤立 `tool_call`（尚无 result）不投影
- `assistant_message` → assistant，带累积 thinking，然后清空 thinking 缓冲
- `assistant_delta`：累加 `thinking` 到缓冲；`text` 不投影（正文以锚点为准）

导出 Markdown（`ExportSessionMarkdown`）继续「与 Replay 同源」，因此导出将自动含工具小节。更新 C-SES-10 锁定测试的期望文本。

### 6.2 事件桥

`llm.ToolEvent` 增加 `Content string`（全文；若超过 64KiB 则按 rune 边界截到 64KiB 并加截断标注）。`Summary` 仍为 ≤200 字芯片摘要。Wails 事件 `chat:tool` 增加 `content`。`chat:chunk` 的 `thinking` 字段本来就有，前端改为写入 store。

### 6.3 前端

依赖：`marked` + `dompurify`（仅此二者；不加高亮库）。

- 助手 `content` 经 `renderMarkdown`：`marked.parse` → `DOMPurify.sanitize`。消毒失败或抛错 → 回退为纯文本（`whitespace-pre-wrap`）。用户消息、工具全文 **不** 走 markdown。
- 禁止未消毒 `v-html`。`renderMarkdown` 单独模块 + vitest：输入含 `<script>` / `javascript:` 的样本，输出不得含可执行标签。
- thinking：助手卡上方可折叠「思考」。`streaming && thinking` 时默认展开；收到终态后默认折叠。用户手动展开的状态不强制记住（无持久化）。
- 工具卡：默认芯片（名 + 200 字摘要 + 状态点）；点击展开/收起下方 `<pre>` 全文（`max-height` 滚动）。`status=error` 保持现有错误色。
- `chat` store：`onChunk` 累加 `thinking`；`onTool` 写入 `content`；`selectSession` 映射 Replay 新字段。
- 视觉令牌不改，不加新主色。折叠控件用现有 `chip` / 文字色。

## 7. 错误处理

| 场景 | 行为 |
| --- | --- |
| 旧账本无 id | 合成 `call-{seq}`，按 §4.3 配对，不报错 |
| 半截 tool_call 无 result | 不进入模型上下文；UI Replay 不显示该卡 |
| list 越界 / 非目录 | `IsError`，可读中文或英文原因（与现有 fstool 风格一致），零修改 |
| search 越界 | 同上 |
| search 坏正则 / 坏 glob | `IsError`，不抛 panic |
| search 超时有部分命中 | 返回部分输出 + 超时标记 |
| markdown 消毒失败 | 纯文本回退，不白屏 |
| 无激活渠道 | 保持现状：Send 可读错误，UI 引导设置 |

失败路径必须上抛到 UI（C-APP-1 / R2）。工具业务失败走 `IsError`，不把业务失败变成编排层 `error`。

## 8. 契约与测试

TDD：每条先写失败测试再写实现。测试名与 ID 一致。

### C-AGT 模型上下文回放（新组）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-AGT-1 | 第二轮 `Run` 的请求消息含上一轮 `assistant(tool_calls)` + `role=tool`，ID 配对 | `TestAgent_DerivesToolHistoryAcrossTurns` |
| C-AGT-2 | 单条 tool 结果 >4096 字节时模型侧截断并含 `truncated` 标注；账本仍是全文 | `TestAgent_TruncatesToolResultForModel` |
| C-AGT-3 | 无对应 result 的 tool_call 不进入模型消息 | `TestAgent_OmitsUnpairedToolCall` |
| C-AGT-4 | 缺 `id` 的旧事件能合成 ID 并配对，第二轮请求协议合法 | `TestAgent_SyntheticIDsForLegacyLedger` |

### C-FS 增补

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-FS-5 | `list` 越界或非目录 → `IsError` 且工作区零修改 | `TestFSList_RejectsEscapeAndNonDir` |
| C-FS-6 | `list` 最多 500 条，超出截断并标注总数 | `TestFSList_OutputBounded` |
| C-FS-7 | `list` 只列下一层（子目录内容不出现） | `TestFSList_NonRecursive` |

### C-SEARCH（新组）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-SEARCH-1 | 路径越界拒绝 | `TestSearch_PathEscapeRejected` |
| C-SEARCH-2 | 匹配数与总字节均有界，超限截断并标注 | `TestSearch_OutputBounded` |
| C-SEARCH-3 | 无效正则 → `IsError` 且原因可读 | `TestSearch_InvalidPattern` |
| C-SEARCH-4 | 跳过二进制与内置忽略目录；显式 `path=vendor` 仍搜该根 | `TestSearch_SkipsIgnoredAndBinary` |
| C-SEARCH-5 | 零匹配成功，`Content` 含 `no matches` | `TestSearch_NoMatchIsSuccess` |
| C-SEARCH-6 | 超时预算内返回；有部分命中则带已捕获输出 | `TestSearch_TimeoutPartial` |

### 编排 / 前端

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-APP-3 | `Replay` 投影含 tool 卡（name/status/content）与 assistant thinking | `TestChatService_ReplayIncludesTools` |
| C-SES-10 | 指向不变；期望随 Replay 含工具而更新 | 现有 `TestChatService_ExportMarkdown` 补工具片段 |
| — | 前端：thinking 累加；终态后可折叠语义由组件承担；消毒去掉 script | `chat.test.ts` 增补；`markdown.test.ts` |

时序类 search 超时测试遵循 `TESTING.md`：轮询直到条件成立或超时，不用「固定 sleep + 单点墙钟断言」。

## 9. 文档与发布

同一实现批次更新：

- `docs/CONTRACTS.md`：登记 C-AGT / C-FS-5~7 / C-SEARCH / C-APP-3；契约变更记录
- `docs/ARCHITECTURE.md`：工具表加上 `search`；对话流注明跨轮 derive
- `docs/MILESTONES.md`：新增 M6 及出口标准
- `docs/TESTING.md`：契约清单加 M6
- `docs/PENDING.md`：本轮交付与未做项
- `docs/adr/0007-tool-result-model-truncation.md`：截断发生在 derive 视图、不改账本的决策
- `README.md`：内置工具表加 `search` 与 `fs list`
- `VERSION`：`0.2.0`

明确仍不做（沿 PENDING「刻意不做」）：多协议、git 写、wails CLI、非 Windows。

## 10. 实现顺序（供计划拆分）

1. C-AGT-1~4 测试 → deriveMessages + 写账本带 id
2. C-FS-5~7 测试 → `fs.list`
3. C-SEARCH-1~6 测试 → `searchtool` + `newRegistry`
4. C-APP-3 + 导出测试 → Replay DTO + 事件桥 `Content`
5. 前端 markdown / thinking / 工具展开 + vitest
6. 文档 + VERSION
7. 门禁：`gofmt` / `go test ./...` / `arch_check` / `npm test` / `npm run build`
8. `scripts/release.ps1`

每步可独立提交；不允许「先实现后补测试」。
