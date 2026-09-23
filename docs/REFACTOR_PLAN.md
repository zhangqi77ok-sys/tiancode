# tiancode 重构方案（可落地版）

- 版本：v1.0（2026-09-23）
- 状态：待评审 → P0 开工
- 参照系：`new-api`（d:/weihu/new-api，生产级 Go LLM 网关）、`deepseek-harness-java`（d:/weihu/deepseek-harness-java，下称 dsh-java）
- 前置结论：不换语言、不推倒重来。Go 留守，14.7k 行源码 + 7k 行测试全部保留。

---

## 0. 结论先行

1. **病根是同一个**：四个基础环（对话流式 / 文件编辑 / 命令 git / 会话恢复）全不牢，不是四个独立 bug，而是核心环缺少统一的"失败也一致"的行为纪律；外围机器（harness/rail/热插拔/MCP）是在基础不牢时长出来的防护层，防护层又加重了复杂度。
2. **借 new-api 三样**：供应商 Adaptor 归一化边界、StreamScannerHandler 流式中继纪律、统一错误载体。
3. **借 dsh-java 三样**：端口-适配器分层、SessionLog 事件账本、SSE 四帧协议设计。其余（27 个限界上下文的仪式感、计费、多租户、渠道权重索引）一律不抄。
4. **冻结清单**：harness、rail（astguard）、热插拔、MCP 新增能力、arch_check 新规则——停止投入，代码保留不删。
5. 路线：**P0 基础环硬化（1 周）→ P1 分层收敛（1–2 周）→ P2 协议转正（1 周）→ P3 壳替换（触发才做）**。

---

## 1. 现状诊断：四个基础环的翻车点（已核实的证据）

| 基础环 | 翻车点 | 证据 | 根因归类 |
| --- | --- | --- | --- |
| 对话/流式 | 上游挂起 = 永久卡死：`scanner.Scan()` 阻塞无空闲看门狗、无总超时 | `plugins/provider/openai/openai_provider.go:261-281` | 无流式纪律 |
| 对话/流式 | 消费方卡住时 `outChan <-` 发送阻塞，仅发送**前**检查 ctx（267-271），goroutine 泄漏 + 上游连接悬挂 | 同上 | 无发送逃生 |
| 对话/流式 | `scanner.Err()` 循环后未生产性处理，连接中断静默结束，无 EndReason 区分 正常结束/中断/超时 | 同上 | 无终态契约 |
| 会话恢复 | Windows 下 `os.Rename` 覆盖失败仅返回错误，注释里承诺的"备份式替换"**从未实现** | `internal/session/store.go:399-402` | 持久化脆弱 |
| 会话恢复 | 持久化错误被静默丢弃（`_ =`），丢消息用户无感 | `app_chat.go:282,503` | 错误不可见 |
| 会话恢复 | 助手消息仅在整轮结束后落盘（503），中途崩溃 = 丢整轮回答；会话文件损坏则 Unmarshal 全挂 | `app_chat.go:503`、`store.go:496-499` | 无增量落盘 |
| 命令/git | 前台命令 60s 硬超时 + `taskkill /F /T` 强杀：`npm install`/`go build` 必死 | `plugins/tool/terminal/terminal_tool.go:272,283-289` | 超时不可配 |
| 命令/git | daemon 模式无超时、`logBuf`（bytes.Buffer）内存无界 | `terminal_tool.go:216-270,230` | 资源泄漏 |
| 文件编辑 | `AtomicWriteFile` 本身合格（temp+Sync+Rename+回退，`sandbox/fs.go:82-139`）；但 astguard rail 写前拦截会误拦正常写入 | `plugins/rail/astguard` | 防护层反噬 |

复杂度盘点：15 个子系统（loop/memory/host/harness/sandbox/mcp/session/rails/热插拔/arch_check/…），核心环只占约两成代码却从未被逐一打牢。

---

## 2. 参照系一：new-api —— 借它的"流式中继纪律"

### 2.1 它的分层（实测）

```
main → router → middleware(distributor 鉴权/选渠道)
             ↘ controller → relay → relay/channel/<vendor>(adaptor) → 上游 HTTP
       controller → service(选渠道) → model(DB/缓存)
       relay/* → relaykit(归一化 DTO + 统一错误)   [全向下依赖，无反向]
```

### 2.2 借三样（均带实测证据）

1. **Adaptor 归一化边界**：`relay/channel/adapter.go:17-34` —— 接口 = Init / GetRequestURL / SetupRequestHeader / `ConvertOpenAIRequest`（入站统一 → 厂商私有）/ DoResponse（厂商 → 统一 usage + 统一错误）。注册是简单工厂 `relay_adaptor.go:50`。**加一个新厂商 = 一个文件**。
2. **StreamScannerHandler 流式纪律**：`relay/helper/stream_scanner.go:77-310` ——
   - scanner goroutine 读上游 → dataChan → 独立写 goroutine，读写分离；
   - `stopChan` + `cleanupOnce` + `sync.Once` 保证单次清理、`wg.Wait()` 等全部退出；
   - **空闲超时**：每收到一行 `ticker.Reset(streamingTimeout)`（:250）；
   - **写截止**：单次写阻塞上限 30s `SetWriteDeadline`（:29-33）；
   - **EndReason 追踪**：正常 / ScannerErr / **ClientGone**（:283-304），客户端断开立即 `resp.Body.Close()` 反压上游停止生成；
   - 湍流兜底：缓存 lastStreamData、流内 error 块透传、无 usage 本地估算。
3. **统一错误载体**：`relaykit/types/error.go:90-107` —— `NewAPIError` 含 `skipRetry` 标志，支持 `errors.Is/As`；`ToOpenAIError()/ToClaudeError()`（:180-208）按客户端协议变形输出并脱敏。

### 2.3 明确跳过

计费/配额预扣-退款、渠道权重优先级内存索引 + auto-ban + 多 Key 轮换（`channel_cache.go`）、管理后台（web/ React 面板）。单人桌面工具用不上，复杂度反而是负资产。

### 2.4 tiancode 对照

tiancode 已有 provider 雏形：`plugins/provider/{openai,ollama,grok}`，`StreamChat(ctx) <-chan v1.StreamChunk`（ollama/grok 委托 openai base）。**缺的正是纪律层**：无空闲看门狗、无发送逃生、无 EndReason、错误不上抛到 UI。重写范围 ≈ 一个文件 + 一个字段。

---

## 3. 参照系二：dsh-java —— 借它的"账本与协议"

### 3.1 它的分层

`app → trigger(19 Controller) → api(Facade) → case(用例/策略树) → domain(27 上下文/端口) ← infrastructure`；`domain → types(SPI，不依赖 Spring) ← plugins`。

### 3.2 借三样

1. **端口-适配器**：Domain 只定义端口（`ILlmRuntimePort` 等 15+），Infrastructure 实现。tiancode 已按此收敛过一轮（T0-T2），保持并强化。
2. **SessionLog 事件溯源**：sealed 事件（TurnStart/TurnEnd/StepStart/UserMessage/AssistantChunk/ToolCall/ToolResult…）追加式账本，回放、审计、UI 投影三合一。**这是 tiancode 会话恢复的治本方案**：JSON 全量重写（现 `store.go`）→ 事件追加账本，崩溃恢复 = 重放到最后一个完整事件。
3. **SSE 四帧协议**：`meta / chunk / step_break / done`，`step_break` 是独立事件而非文本内嵌标记（避免多步工具调用气泡乱码）。tiancode 的前端工具卡片渲染可直接对齐。

### 3.3 引以为戒（它自己 README §14 承认的坑）

无界 `newCachedThreadPool`、`deriveMessages` O(n²)、token 用 `length/4` 粗估、运行期审批 gate 默认未启用、`shell_execute` 零沙箱。**分层的完备 ≠ 运行时的完备**——这是 tiancode 最该吸取的教训：先修运行时行为，再谈层次仪式。

---

## 4. 目标架构分层（tiancode v2）

```
┌──────────────── 壳 shell（可换） ────────────────┐
│  Wails 薄壳（现） / 浏览器 / 未来 Electron|Tauri   │
└───────────────────────┬─────────────────────────┘
                        │ Wails 绑定 或 HTTP/SSE /api/v1
┌───────────────────────▼─────────────────────────┐
│ 协议网关 gateway                                  │
│  internal/transport/http + backend/cmd/tcode-daemon│
│  统一：超时 / 取消 / 错误帧 / EndReason / 本地 token │
├─────────────────────────────────────────────────┤
│ 用例编排 orchestrator（从 package main 迁出）       │
│  对话用例 / 工作区用例 —— 只编排，禁业务规则          │
├─────────────────────────────────────────────────┤
│ 内核 core（无状态、可独立测试）                      │
│  loop     ReAct 引擎（T0-T2 已收敛，保持）           │
│  llmgw    供应商端口 + 流式中继纪律 ← 借 new-api      │
│  toolreg  工具注册表 + 执行契约（超时/取消/部分输出）   │
│  sessions 会话聚合 + 事件账本 ← 借 dsh-java          │
├─────────────────────────────────────────────────┤
│ 适配器 adapters（实现端口，允许依赖内核端口）          │
│  plugins/provider/*（openai/ollama/grok）          │
│  plugins/tool/*（fs/terminal/git/search/arch）     │
│  sandbox（原子写/快照）                             │
├─────────────────────────────────────────────────┤
│ SPI：pkg/plugin/v1 + rail（冻结，默认关闭）          │
└─────────────────────────────────────────────────┘
```

### 依赖规则（落到 arch_check）

| 规则 | 内容 |
| --- | --- |
| R9（新增） | 内核 core 不得 import 壳/网关/适配器；适配器只准依赖 core 端口与 pkg/plugin/v1 |
| R10（新增） | 所有 mutating 工具必须有超时/取消/部分输出契约（检测 Execute 内 `context.WithTimeout` 或显式透传） |
| 既有 | R7 宿主层禁含窗口逻辑、R8 工具注册幂等 —— 继续生效 |

### 现有文件 → 目标位置

| 现状 | 去向 | 动作 |
| --- | --- | --- |
| `app.go` / `app_chat.go` / `app_vcs.go` / `app_shell.go` 编排逻辑 | `internal/orchestrator` | P1 迁出，wails 绑定变薄 |
| `plugins/provider/*` | 适配器（实现 `llmgw.ProviderPort`） | P0 补纪律，P1 定端口 |
| `internal/transport/http` + `backend/cmd/tcode-daemon` | 网关（已是雏形） | P2 转正，endpoint 版本化 |
| `internal/session/store.go` | `core/sessions` + 事件账本 | P0 修致命点，P2 换账本 |
| `plugins/rail/astguard` | 冻结 | P0 默认关闭 |
| `internal/core/harness` / 热插拔 | 冻结 | 停止投入 |

---

## 5. 落地路线图

### P0：基础环硬化（最高优先，约 1 周）

> 出口标准：四环各有一份"失败也一致"的行为契约 + 测试锁定，`go test ./...` 绿。

| # | 任务 | 改动点 | 做法 | 验收 |
| --- | --- | --- | --- | --- |
| P0-1 | 流式纪律 | `plugins/provider/openai/openai_provider.go`、`pkg/plugin/v1`（StreamChunk 加 EndReason 字段） | ① 空闲看门狗：N 秒无新行 → Error chunk（EndReason=IdleTimeout）；② 循环后检查 `scanner.Err()` → 错误帧；③ `outChan <-` 改 `select { case outChan<-c: case <-ctx.Done(): }` 发送逃生 | httptest 模拟三种故障（挂起/中断/慢消费）三测试全绿 |
| P0-2 | 会话持久化 | `internal/session/store.go:399-402`、`app_chat.go:282,503` | ① 实现注释承诺的备份式替换（rename 失败 → 备份旧文件 → 重试 → 失败回退直写并告警）；② `_ =` 改为 失败 → 日志 + 一次重试 + 用户可见警告；③ 助手消息在 turn 结束/出错/取消三点落盘；④ 会话文件损坏时降级恢复（截断到上一个完整消息 + 告警） | Windows rename 冲突注入测试；mid-turn kill 恢复测试 |
| P0-3 | 命令超时契约 | `plugins/tool/terminal/terminal_tool.go` | ① 超时可配（默认提到 120s），按需覆盖；② 超时返回已捕获的**部分输出** + 明确 `TIMEOUT` 标记，不静默；③ daemon `logBuf` 加上限（超限截断并标注） | `sleep` 超时测试返回部分输出；长命令引导用 `background=true` |
| P0-4 | 护栏降级 | `app_hotplug.go` / rail 注册处 | astguard 默认 **off**（或 warn 模式只记日志不拦截），配置显式开启 | 默认配置下写"临时非法语法"的 Go 文件不被拦截 |

### P1：分层收敛（1–2 周）

1. `package main` 编排逻辑迁入 `internal/orchestrator`，`app_*.go` 变薄绑定层。
2. `internal/core/loop`（或新 `core/llmgw`）定义 `ProviderPort`，`plugins/provider/*` 实现之——接口形态抄 new-api：`ConvertRequest` / `DoResponse` 归一化边界，加新厂商 = 一个文件。
3. `go mod vendor` + 前端 dist 门禁修复：离线/新环境 `go build ./... && go test ./...` 可跑。
4. 验收：arch_check 含 R9/R10 全绿；离线可构建。

### P2：协议转正（1 周）

1. `backend/cmd/tcode-daemon` 成为内核唯一入口：`tiancode serve` = HTTP/SSE on 127.0.0.1:8765。
2. endpoint 版本化 `/api/v1/*`；SSE 事件对齐 dsh-java 四帧（meta/chunk/step_break/done）+ 每帧带 EndReason 语义；本地 loopback token 认证。
3. 会话存储升级为**事件追加账本**（借 dsh-java SessionLog）：崩溃恢复 = 重放到最后完整事件；JSON 全量重写只作导出格式。
4. Wails 壳切到 HTTP 或暂留绑定（决策点，二选一，不影响内核）。

### P3：壳替换（触发才做）

触发条件（3 个月后复核）：roadmap 中 ≥3 个大特性依赖 npm-only 生态 / 深度编辑器集成 / 高频编排实验。届时沿 `/api/v1` 从壳开始迁移，内核最后动。

---

## 6. 不变量与守卫

1. **冻结清单**：harness、rail、热插拔、MCP 新增能力、arch_check 新规则——P0/P1 期间零投入。
2. **每个 P0 修复必须带最小测试**，测试名即行为契约（如 `TestStreamChat_IdleTimeout_ReturnsErrorChunk`）。
3. arch_check 继续作为提交门禁，P1 增加 R9/R10。
4. T0-T5 已固化的一切（单点注册、构造注入、幂等）不许回退。

## 7. 明确不做

- 语言重写（Rust/TS/C#）：gate 未触发，方案 C 的协议边界已把该决策变成可逆项。
- 多租户 / 计费 / 渠道权重调度 / 管理后台：单人工具的负资产。
- 27 个限界上下文式 DDD 仪式：分层以"编译期可查的依赖规则 + 行为契约测试"为准，不以包数量论英雄。
