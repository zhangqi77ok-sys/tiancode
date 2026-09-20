# 湉码 / tiancode

> **热插拔插件化桌面 AI Coding 工作台**。名字来自 **湉**：水面平静、水流安稳。  
> **当前版本**：`v0.0.1 (Alpha)` — 核心处于早期开发测试、架构加固与需求孵化阶段。  
> 基于 **Wails v2 + Go 原生微内核 + Vue 3 + TypeScript**，采用 **收敛单执行回路内核**、**Rail 物理安全防线** 与 **热插拔工具注册表**。对标专业级桌面 coding agent，提供透明、可控、高可靠的智能编程交互体验。

---

## 🧭 项目导航与核心入口

* **项目仓库**：[https://github.com/zhangqi77ok-sys/tiancode](https://github.com/zhangqi77ok-sys/tiancode)
* **工程文档与 54 篇知识库总览**：[`docs/README.md`](docs/README.md)
* **56 次核心架构演变与工程迭代详实记录**：[`docs/ARCHITECTURE_EVOLUTION.md`](docs/ARCHITECTURE_EVOLUTION.md)
* **底层工程知识与疑难解决方案速查**：[`docs/knowledge/README.md`](docs/knowledge/README.md)
* **AI 交付实现唯一施工合同**：[`docs/AI_IMPLEMENTATION_CONTRACT.md`](docs/AI_IMPLEMENTATION_CONTRACT.md)
* **确定性前缀与 KV Cache 技术规约**：[`docs/PROMPT_CACHING_SPEC.md`](docs/PROMPT_CACHING_SPEC.md)
* **特性边界矩阵与交付状态**：[`docs/V1_FEATURE_BOUNDARY_MATRIX.md`](docs/V1_FEATURE_BOUNDARY_MATRIX.md)

---

## 🏛️ 一、核心架构设计 (Single Convergent Architecture)

湉码 严格遵循单一主轴架构，打破二套循环与过度封装，将微内核执行引擎、安全防线、工具注册表与表现层状态彻底解耦：

```text
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                              湉码 Frontend (Vue 3 + TypeScript + Pinia)               │
│   [ 单一真实源 Store: workbench.ts (会话、待确认Diff池、项目宪法、终端状态统一调度) ]       │
│   [ 单焦点主工作区 (智能对话 / Monaco编辑器 聚合切换) | Diff 对比 | 终端抽屉 | 纯净空状态 ]  │
│   [ 待采纳代码变更阻断横幅 | 严格居中暖色模态窗体系 | 16:9 人机工学空间视野 ]              │
└───────────────────────────────────────────┬────────────────────────────────────────────┘
                                            │ Wails v2 原生 IPC / Typed Event Streams
                                            ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                          湉码 Go Native Microkernel (Wails v2 Engine)                 │
│                                                                                        │
│  ┌──────────────────────────────────────────────────────────────────────────────────┐  │
│  │                    Session 最小任务模型 (Session Task Model)                      │  │
│  │    Goal ➔ Status ➔ ToolBudget ➔ ToolsUsed ➔ Summary ➔ TDDPassed ➔ PendingDiffs   │  │
│  │    支持「继续」指令无损接续历史任务目标，严禁推翻重勘或无序全库遍历                │  │
│  └────────────────────────────────────────┬─────────────────────────────────────────┘  │
│                                           │                                            │
│  ┌────────────────────────────────────────▼─────────────────────────────────────────┐  │
│  │                 Single Execution Engine (单一收敛直连执行内核)                    │  │
│  │    • 生产级统一直接调用，根除二套循环，消除「两套上限、两套收尾」隐患              │  │
│  │    • AI 自主交付判定 (Zero Tool Calls 即完成) + 20 轮防爆兜底 + 连续 3 次熔断     │  │
│  │    • 四维人话结尾保障：触顶总结、用户中止、上游 4xx/5xx 转人话、空输出保全        │  │
│  │    • 审查任务铁律：先看地图（顶层结构+关键入口），再定靶向下钻，禁止盲目全库扫描 │  │
│  │    • TDD 策略刚性约束：测试未全绿则任务状态绝对不是完成，阻断虚假宣称               │  │
│  └────────────────────────────────────────┬─────────────────────────────────────────┘  │
│                                           │ 拦截链 (Rail.OnBeforeAct / OnAfterAct)     │
│  ┌────────────────────────────────────────▼─────────────────────────────────────────┐  │
│  │                       Safety Rail (生产挂钩的物理安全防线)                        │  │
│  │    • OnBeforeAct: 越界高危命令拦截、只读策略写入阻断、敏感凭据脱敏                │  │
│  │    • OnAfterAct: 物理执行审计与写后影子 Git 快照记录                              │  │
│  └────────────────────────────────────────┬─────────────────────────────────────────┘  │
│                                           │ 通过 host.Registry 统一定位与分发          │
│  ┌────────────────────────────────────────▼─────────────────────────────────────────┐  │
│  │               Hotplug Plugin & Tool Registry (微内核热插拔算子体系)               │  │
│  │    • tool.fs (沙箱读写/列表/原子写)  • tool.search (工作区 grep & find 检索)      │  │
│  │    • tool.git (版本控制与子仓库保护) • tool.terminal (CREATE_NO_WINDOW 静默执行)  │  │
│  │    • MCP Manager (标准 JSON-RPC 2.0 stdio / sse 协议)                              │  │
│  │    • provider (OpenAI / Claude / Gemini / Grok / Azure / Ollama 6 大驱动)         │  │
│  └──────────────────────────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

### 依赖倒置架构铁律
微内核严格遵循单向依赖，禁止业务层直接持有具体 Tool 实例，新增算子无需修改任何路由代码：
```text
plugins/ → pkg/plugin/v1 ← internal/ ← app.go
```

---

## ⚡ 二、核心功能与技术亮点

### 1. 单一执行内核收敛与双重智能熔断器
* **AI 自主完成判定**：在 ReAct 回路中，当大模型判断目标达成（未再发起工具调用）并向用户输出文本时，系统判定任务自主圆满交付；
* **防脱缰双重主动熔断**：
  * **20 轮极端安全保险丝**：作为底层死循环安全防线；
  * **连续 3 次重复调用主动熔断**：追踪工具指纹，防止模型陷入原地死循环空耗 Token；
  * **连续 3 次报错主动熔断**：工具连续执行失败立即终止并引导用户排查；
* **四维人话收尾机制**：在触顶熔断、手动中止、上游 4xx/5xx 报错及空包等任何异常退出场景下，均以清晰的人话汇报成果与下一步建议，杜绝“突然没了”。

### 2. 多协议模型服务网关与极简鉴权 (对齐 new-api 体验)
* **6 大原生协议驱动**：内置 OpenAI（含中转及第三方端点）、Anthropic Claude（Messages API / Thinking 思考流）、Google Gemini（AI Studio 及 OpenAI 兼容）、xAI Grok（含 4.6 深度推理）、Azure OpenAI 与 Ollama 本地驱动；
* **统一凭据输入框 (对齐 new-api 体验)**：
  * **单 Key / 多 Key 模式**：直接粘贴 `sk-...` 或 `AIzaSy...`，支持换行多 Key 自动轮询调度；
  * **Google 凭据直贴**：直接粘贴 GCP 凭据 JSON（`application_default_credentials.json` 或 Service Account JSON），内核自动解构 `client_id`、`client_secret`、`refresh_token` 并自动换票保活；
  * **Codex / ChatGPT OAuth**：支持直接粘贴 OAuth JSON 或单行 Refresh Token；
  * **本地直连**：支持 Ollama 及内网服务免鉴权直连；
* **毫秒级真实探活与健康指示灯**：顶栏指示灯严格绑定主渠道真实探活状态（在线绿灯带延时、待测琥珀、离线红灯、未配灰灯、推理橙色脉冲），杜绝虚假在线。

### 3. 代码修改 Diff 审查、Monaco 多页签与 Hunk Cherry-Pick
* **改动带出 Diff 审查**：智能体写入代码时自动唤起 Monaco Diff 对比视窗，红绿对照展示前后变更；
* **行级 Hunk 分块与 Cherry-Pick**：右侧审查区将 Diff 结构化切分为独立变更块，每个块独立支持 `[✓ 采纳块]`（`git apply --cached` 暂存入 Index）与 `[✕ 丢弃块]`（`git apply --reverse` 局部无损反向还原）；
* **发送前强拦截**：存在未确认的 `PendingDiffFiles` 时，发送下一句指令自动呼出居中拦截弹窗，防止盲目覆盖代码；
* **嵌套子仓库防崩溃**：对包含未提交嵌套 Git 仓库执行暂存时自动前置探活，杜绝 Exit 128 异常，并清洗尾部斜杠。

### 4. 真实双栈 TDD 自动化验证与结构化失败清单
* **双栈级联实测**：打破单语言互斥偏见，同时级联执行 Go 原生测试 (`go test -v ./...`) 与 Node 前端测试 (`npm test`)；
* **结构化失败清单提取**：自动提取 `FAIL: TestX` 或 Jest/Vitest 失败用例名，置顶呈现 `FailedTests` 列表；测试未全绿则任务绝对不算完成，杜绝假成功。

### 5. 一等工作区检索算子与受控集成终端
* **一等检索算子 (`tool.search`)**：原生 Go 算子提供 `grep`（文本/代码正则匹配）与 `find`（文件名通配），强制 AI 先检索再下钻；
* **开发者全局检索视窗**：侧边栏支持 **📁 目录** 与 **🔍 检索** 双模切换，检索结果一键直跳 Monaco 编辑器行号；
* **Windows 零黑框终端 (`CREATE_NO_WINDOW`)**：以 `0x08000000` 静默运行外部进程，杜绝黑色控制台闪烁弹窗；终端抽屉快捷键 **`Ctrl + \``** 极速唤起；支持进程树级联销毁（`taskkill /F /T`）防孤儿挂死。

### 6. GitOps 源码控制中枢与纯净零数据
* **双层暂存分离**：`Staged Changes` 与 `Working Changes` 双层清晰呈现，与底层 Git 状态实时同构；
* **微内核影子快照**：Agent 写代码前 $\le 5\text{ms}$ 自动生成轻量 Git 影子快照，支持秒级无损回退；
* **纯净零假数据 (Zero Demo Policy)**：空项目、初次启动绝无任何硬编码伪造数据与假消息。

### 7. 插件热插拔中心与 DSH 算子大盘 (DeepSeek Harness Hub)
* **一等公民常驻工作台入口**：左侧活动栏常驻 `🧩` 快捷入口、对话顶栏实时显示 `🧩 算子大盘 (N)` 状态指示胶囊，并支持全局快速检索 `Ctrl+K`（`/hotplug`）毫秒级唤起；
* **微内核算子全景拓扑**：直接查询 Go 微内核 `host.Registry` 与 `mcp.Manager`，真实罗列所有已挂载算子（`tool.fs`、`tool.git`、`tool.terminal`、`tool.search`、`tool.ask_user` 等），支持在线下钻查看各算子的大模型 JSON Schema 参数契约与读写/只读属性；
* **单点探活与即时健康探针**：支持针对任意算子、协议驱动或 MCP 外部进程发起物理探活，真实测量首字延时 (TTFT) 与状态，严格 Fail-Closed；
* **SafetyRail 核心防线大盘**：透明可视化呈现 P-100 终极阻断权防线机制（危险命令拦截、目录逃逸沙箱防护与 API Key 凭据脱敏）；
* **动态热重载与 DSH 技能造物主 (Creator Mode)**：支持一键重新挂载与同步外部 MCP 进程与算子；集成 PRD §4.14 DSH 技能造物主模式，支持现场快捷定义新技能 (Skill)、追加工程规则 (Rule) 与热挂载外部算子，实现真正意义上的插件热插拔闭环。

### 8. 代码架构与依赖治理工作板 (Code Architecture & Dependency Workbench)
* **从玩具级 AST 到现代化架构治理**：彻底淘汰旧版 24 轮前端力导向随机排布，基于 Go 官方编译前端（`go/parser`, `go/token`, `go/ast`）全量分析工作区 AST 语法树，`<30ms` 极速完成代码分层与依赖提取；
* **六层语义拓扑 DAG 与 4 列流式折行 (Layered DAG & Adaptive Flow)**：根据微内核架构自动划分为 `entry`、`host`、`core`、`bus`、`spec`、`tool`、`other` 七大层级，采用 4 列网格自适应多行流式折行与动态层高排布，彻底杜绝单行无限向右延伸，自适应 16:9 原生工作台；
* **隐式接口契约多态矩阵 (Contract Matrix)**：基于 Duck Typing 签名匹配算法，自动将所有抽象接口（Interface）与具体实现结构体（Struct）进行多态匹配与覆盖率透视，呈现 100% 依赖倒置原则；
* **符号级影响面精准雷达 (Symbol-level Blast Radius & Call Sites)**：输入任意核心结构体、方法或接口，深入 AST SelectorExpr 与 Ident 解析直接调用者、精确到文件行号与上下文代码切片（CallSites）、间接传递波及包以及关联需要回归的单测清单；
* **影响面回归测试一键执行 (In-Place Test Runner)**：雷达看板中推荐的测试用例支持 `[▶ 运行]` 按钮，一键调度底层终端抽屉以 `CREATE_NO_WINDOW` 实时流式运行 `go test -v ./<pkg>/...` 并即时呈现通过状态；
* **符号与契约单击直跳 Monaco (Click-to-Source in Monaco)**：接口契约、实现类、抽屉导出符号、影响面调用点全链路支持一键单击直跳 Monaco 编辑器，毫秒级定位对应代码文件与行号高亮；
* **微内核 `tool.arch` 算子闭环 (Agent Autonomous Architecture Tool)**：实现标准的 `pkg/plugin/v1.ToolPlugin` 插件，向大模型自动暴露 `code_architecture` 算子（`inspect`、`blast_radius`、`discover_modules`），彻底赋能自主 Coding Agent 在代码修改或重构前自发调用评估；
* **铁律 7 架构防腐守卫 (Architecture Rail)**：实时扫描依赖关系并对违规导入（如插件反向依赖 core）进行危险红线告警，支持“仅看违规”一键过滤；
* **Agent 上下文双向飞轮**：活动栏（`🏛️`）与对话顶栏常驻入口，支持一键将当前架构分层、依赖关系与 ADR 规范格式化注入 AI Agent 提示词，实现由架构指导开发、由测试保障发货的正向闭环；
* **Monorepo 多子模块与外部项目独立探查 (含防投毒守卫)**：顶栏集成模块选择器，自动探测工作区内部所有独立 `go.mod` 模块与 `cmd/` 入口程序；支持通过原生文件夹选择框独立解析任意外部本地 Go 仓库；在外部模式下自动物理禁用“注入 Agent”功能，防止上下文投毒，关闭弹窗时自动瞬态重置回主工作区。

### 9. 确定性前缀冻结、KV Cache / Prompt Caching 与真实遥测大盘 (Deterministic Prefix Freezing & KV Cache Telemetry)
* **7 层确定性 Token 排布流水线 (The 7-Layer Deterministic Token Pipeline)**：解构大模型推理前缀，将 Token 序列按易变性严格分为 `Layer 0~1 静态基座`、`Layer 2 规范化工具 Schema`、`Layer 3 工作区 AST 架构指纹`、`Layer 4~5 线性历史与修剪块` 以及 `Layer 6 动态瞬态尾部`，实现多轮 Coding 会话中公共前缀 KV Cache 命中率稳定维持在 **85% ~ 95%**；
* **Canonical JSON 递归键排序与 CRLF 归一化**：提供 `protocol.MarshalCanonical` 与 `protocol.CanonicalizeValue`，消除 Go `map[string]any` 遍历随机哈希与 Windows `\r\n` 跨平台字节差异，确保两次相同请求的 JSON 载荷与 SHA-256 绝对一致；
* **动态上下文物理沉底隔离 (Zero Prefix Contamination)**：彻底终结将任务接续上下文（`continuationCtx`）、技术栈探测（`stackPrompt`）与待确认文件拼接进 `systemPrompt` 的“缓存杀手”坏味道；将所有易变上下文收拢进 `<dynamic_context>` 标签，严格沉底作为最新一条 User Prompt 附件；前端智能识别并在用户提问气泡中折叠为 `⚡ 运行时上下文快照`，兼顾提示词确定性与界面极简；
* **双阈值历史工具输出原位折叠修剪 (In-Place Output Pruning)**：彻底废除从头部截断消息的粗暴滑动窗口（防止第 0 个 Token 改变导致全量缓存报废）；当会话轮次 > 3 且单条历史工具输出 > 1,000 字符时，执行原位微创折叠修剪，保持 Message 角色、`tool_call_id` 与时序链路骨架不变，实现 Append-Only 单向正序追加；
* **头部厂商协议级 Prompt Caching 对齐**：
  * **Anthropic Claude**：注入 `anthropic-beta: prompt-caching-2024-07-31` 请求头，在工具列表末尾注入 Breakpoint 1，在倒数第 2 轮 User 消息末尾注入 Breakpoint 2；实时捕获 `cache_read_input_tokens` 与 `cache_creation_input_tokens`；
  * **OpenAI / DeepSeek**：流式请求自动注入 `"stream_options": {"include_usage": true}`，解析 `prompt_tokens_details.cached_tokens` 与 `prompt_cache_hit_tokens`，随 `StreamChunk` 派发；
* **真实用量遥测大盘与顶栏微型指示胶囊 (Zero Demo Policy)**：
  * `internal/telemetry` 提供 `RecordWithCache`，严格从模型实际返回的 `usage` 累加核算命中率与预估节省金额（按平均节省 $0.0018 / 1k cached tokens）；
### 10. 现代 AI IDE 双主轴一体化工作台与高人机工程学视口流转 (Dual-Loop Integrated Workbench & Ergonomic Viewports)
* **解耦“工作内容”与“布局形态”**：彻底纠正过去将“智能对话 / 双栏协同 / 文件与编辑器”混在同一文字分段器中的心智冲突；顶栏全面重塑为现代顶级 AI IDE 规范的**面板视口控制组 (Viewport Controls)**：`[💬 对话专注]`、`[⚡ 双栏协同 (Ctrl+\)]`、`[💻 代码全屏 (Alt+F)]`，单向数据流与单一状态来源调度；
* **消灭“双侧边栏夹心” (Eliminating the Double Sidebar Trap)**：将工程文件目录树（Explorer）与全盘检索彻底归拢搬迁至左侧主抽屉 `LeftDrawer`（会话分支 / 文件管理 / GitOps 统一由左侧 `ActivityBar` 图标点击切换并支持折叠），从 `DiffWorkspace` 中彻底剔除 240px 的内嵌冗余文件树，让右侧 Monaco 编辑器与 Diff 对比独占纯净宽屏舞台；
* **自由拖拽分栏手柄 (Draggable Resizer Sash)**：双栏协同工作台中央嵌入丝滑拖拽中缝，鼠标悬停即现低饱和陶土暖橙抓手，支持在 20% ~ 80% 范围内按需自由缩放对话区与代码区；支持双击中缝一键平分 (50/50) 复位，分栏比例自动落盘持久化至 `localStorage`；
* **代码全屏模式下的防失联 AI 微胶囊 (Zero-Blindspot Floating Mini-Cockpit)**：当用户切入代码全屏专注时，顶栏实时动态挂载微型 AI 状态胶囊，实时感知流式生成进度（`[⚡ AI 生成中... 展开双栏]`）或待确认代码变更（`[📝 待确认变更 (N)]`），点击随时一键切回双栏协同，彻底终结看代码时无法感知 AI 思考的“失联感”；
* **全局人机工学快捷键闭环**：全面支持 `Ctrl + B`（快速折叠/展开左侧抽屉）、`Ctrl + \`（一键切换协同双栏与纯对话面板）、`Alt + F`（代码全屏与双栏无缝切变），实现全程无鼠标沉浸式编码。

### 11. 手术级精准局部补丁算子、Monaco 原生 Diff 审查与全域人机工程学拖拽分栏 (Surgical Code Patching, Native Monaco Diff & Ergonomic Resizers)
* **终结 100% 全量覆写风险之 `fs_control(replace)` 局部算子**：
  * 对齐现代顶级 Coding Agent 标准，彻底终结修改几行代码必须全量输出千行文件的冗余模式；
  * 支持 `target_content`（精确字符特征块）与 `replacement_content` 原子级替换；支持 `start_line` / `end_line` 局部搜索区间约束，以及多处匹配阻断守卫（`allow_multiple: false`）；
  * 替换前由 `SnapshotManager` 自动生成影子 Git 快照，并通过临时文件原子落盘，响应效率提升 80% 并杜绝大模型偷懒导致的源码截断损坏；
* **终端长耗时守护任务支持 (`is_daemon: true`) 与多维生命周期控制**：
  * `exec_command` 扩展非阻塞异步后台常驻模式，彻底消除 `npm run dev`、`vite`、`go run` 等长期服务卡死 60 秒硬超时的硬伤；
  * 提供 `action: "run"`（即时返回 `task_id` 与 PID）、`action: "status"`（实时返回运行时长与尾部日志缓冲区）与 `action: "kill"`（在 Windows 上注入 `taskkill /F /T` 强力清理整棵子进程树，防止孤儿进程挂死）；
* **中文无空格 `@` 引用精准提取与 Token-Safe 上下文对齐裁剪**：
  * 彻底修复旧版本仅靠空格分词导致“`请看@main.go中的逻辑`”等中文 Prompt 无法解析引用的缺陷，引入精准正则提取全字符集路径并自动附入文件内容；
  * 会话窗口容量平滑扩容至 120,000 字符；历史截断算法强制实行 **User 角色对齐准则**（`startIndex` 裁剪后首条消息必须是对齐的 `user` 角色），从根源杜绝工具结果脱节引发的 Anthropic/OpenAI `400 Bad Request` 异常；
* **原生高保真 Monaco Diff Editor 差异对比视窗**：
  * 废除手写 HTML `<div>` 拼接 Diff 模式，全面接入原生 `monaco.editor.createDiffEditor`；
  * 完整具备多语言代码语法高亮、细粒度字符级差异着色、未改动行折叠；支持在顶部工具栏实时在 **双栏并排对比 (Side-by-Side)** 与 **单栏内联对比 (Inline)** 之间无缝切换；
  * 顶部保留轻量 Git Hunk 操作带（`[✓ 采纳块]` / `[✕ 丢弃块]`），实现专业 Monaco 差异审查与敏捷 GitOps 的双重闭环；
* **聊天 Markdown 语法高亮引擎与代码块双向闭环**：
  * 集成 `highlight.js` 与 Atom One Dark 深度主题配色，使大模型输出的各类语言代码块均具备高对比度语法着色；
  * 代码块头部提供一键 `[📋 复制]`（附带视觉反馈）与 `[⚡ 应用至编辑器]`（一键将生成的代码无损注入当前 Monaco 打开的文件中并唤出双栏视口）；
* **全域人机工程学无级拖拽分栏与尺寸记忆**：
  * **左侧资源管理器抽屉 (LeftDrawer)**：右侧边框嵌入垂直拖拽手柄，宽度在 200px ~ 550px 范围内随心调节，双击一键复位 (270px)，尺寸持久化至 `localStorage`；
  * **底部集成终端抽屉 (TerminalDrawer)**：顶部边框嵌入水平拖拽手柄，高度在 140px ~ 75vh 范围内自由拉伸，双击一键复位 (240px)，尺寸持久化至 `localStorage`；
  * **输入胶囊交互治理**：输入框支持 44px ~ 160px 自适应平滑高度扩展，`@` 提及下拉菜单精准锚定在输入卡片正上方，杜绝附件栏撑高造成的弹窗悬空或内容遮挡。

### 12. 后台守护进程管控、受控文件系统 CRUD、会话重命名与暖炭黑 Monaco 编辑器全域治理 (Daemon Task Manager, Sandboxed File System CRUD, Session Renaming & Warm Charcoal Monaco)
* **后台常驻守护进程可视化管控 (Daemon Task Manager & Kill)**：
  * 微内核 `terminal_tool` 契约支持 `action: "list"` 结构化检索活跃守护进程，并支持通过 `action: "kill"` 注入 `taskkill /F /T` 强行阻断整个子进程树并释放端口；
  * 宿主 `app_shell.go` 严格恪守铁律 7 规范，通过 `registry.GetTool("tool.terminal")` 统一派发，无侵入实现守护任务查杀；
  * 终端抽屉顶栏新增 `$_ 终端控制台` 与 `⚡ 守护进程 (Daemons)` 切换标签页、活跃任务计数徽章、实时监控表格与一键 `[■ 终止]` 强行查杀按键；
* **沙箱受控文件树 CRUD 全闭环 (Sandboxed File Tree CRUD)**：
  * 微内核沙箱层 `SafeCreateDir`、`SafeDelete`、`SafeRename` 严格实行盘符大小写归一化与目录越权校验，凡试图使用 `..` 逃逸工作区沙箱的操作一律阻断；
  * 资源管理器顶部工具栏提供新建文件 `＋📄` 与新建文件夹 `＋📁` 快捷按钮；
  * 文件树节点全面支持右键上下文菜单（新建文件、新建文件夹、原地重命名、删除确认、复制绝对路径），无需切出 IDE 即可完整操作项目目录；
* **全域彻底根除原生弹窗与会话原地重命名 (Zero Native Prompt & Session Rename)**：
  * 彻底清除 `LeftDrawer.vue` 内 `window.prompt`，将标签修改收敛至符合暖米白设计规范的居中模态框；
  * 支持会话标题原地重命名，底层由 `session.Store.Rename` 支撑原子落盘并强校验会话存在性；
  * 会话删除引入居中二次确认模态窗，彻底防止误触引发历史会话数据丢失；
* **Monaco 快捷键捕获、全域暖炭黑代码主题与空状态卡片 (Monaco Shortcuts & Warm Charcoal Theme)**：
  * Monaco 编辑器原生通过 `editor.addCommand(CtrlCmd | KeyS)` 捕获保存快捷键，直接触发工作台落盘；
  * 注册并全域应用 `tcode-warm-charcoal` 主题（底色 `#1E1C1A`，行高亮 `#262320`，选区陶土橙高亮 `#D96B2733`），消除冷黑视觉割裂；
  * 单击文件树普通代码文件时默认开启单文件实时编辑视窗；在 Diff 与 Edit 视窗处于空状态时，展示包括快速打开资源管理器、全局检索与新建文件的暖色操作卡片；
* **凭据与会话目录严格权限收敛与双平台 CI 守卫 (Strict Permission Hardening & Dual-Platform CI)**：
  * 全面收敛用户配置目录与会话存储目录访问控制（目录强制 `0700`，配置文件原子写入强制 `0600`），杜绝跨平台环境下 API Key 与工程配置的越权读取；
  * CI 工作流（`.github/workflows/ci.yml`）扩充 `windows-latest` 测试与生产标签编译构建（`-tags "desktop,production"`），确保双平台自动化持续集成防护。
* **视口路由三态精准联动、Windows 路径标准化与全局 Esc 层级闭环 (Viewport Routing & Global Esc Hierarchy)**：
  * `openEditorTab` 在「对话专注」(chat) 模式下打开任意文件时，自动切换到「双栏协同」(split) 模式，彻底消灭编辑器不可见的静默失败；
  * 所有路径在进入前端状态前统一 `replace(/\\/g, '/')` 标准化，修复 Windows 反斜杠导致编辑器 Tab 标题显示完整路径的缺陷；
  * `[⚡ 应用至编辑器]` 按钮：无活动文件时给出 toast 明确引导，有文件但处于 Diff 视图时自动切换到 Edit 视图，彻底消除代码写入后用户看不到的黑洞场景；
  * `handleGlobalKeydown` 全局 Esc 层级补全 9 个文件操作 pending 弹窗状态（`pendingCloseTab`、`pendingDeleteSessionId`、`pendingTagSession`、`pendingRenameSession`、`pendingCreateFile/Folder`、`pendingRenamePath`、`pendingDeletePath`、`fileContextMenu`），严格符合铁律 5；
  * Command Palette 增加暖米白标题行（含搜索图标、说明文字）与显式 `[X]` 关闭按钮，背景色对齐 `#FAF8F5`，居中吸附，全面符合铁律 5 弹窗三维规范。

---

## 🎨 三、视觉与人机工程学规范

湉码 严格执行 Warm Minimalist 暖色极简视觉系统：
* **主背景色**：`#FAF8F5` (Warm Cream 柔和暖米白，消除纯白强反光，保护长时间视力)
* **工作台底色**：`#F4EFEA` (Workspace Muted 米灰)
* **品牌强调色**：`#D96B27` (Terracotta Orange 低饱和陶土暖橙)
* **代码暖黑**：`#1E1C1A` (Code Dark 暖炭黑代码底色)
* **16:9 人机工学空间流转**：视口控制组自由切换与无缝拖拽：
  * `[💬 对话专注]`：全宽长句交互、多轮思维链沉浸阅读；
  * `[⚡ 双栏协同 (Ctrl+\)]`：左侧智能对话 + 中间拖拽手柄 + 右侧 Monaco/Diff 代码审查；
  * `[💻 代码全屏 (Alt+F)]`：代码全屏无干扰阅读，左上角保留微型 AI 脉冲徽章随时唤出；
* **弹窗三维铁律**：全系统模态窗严格居中吸附、统一具备右上角显式 `[X]` 关闭、支持全局 `Esc` 快捷退出与悬停 Tooltip，**严禁使用浏览器原生 `alert()` / `confirm()`**。

---

## 📖 四、快速上手与使用指南

### 1. 安装与启动
* **Windows 单文件安装**：直接运行 `Tiancode_Setup_v0.0.1.exe`，支持图形向导与静默安装参数（`-silent`，`-dir "D:\MyStudio"`）；
* **免安装绿色运行**：直接双击 `bin/tiancode.exe` 启动。

### 2. 配置模型渠道
1. 点击左侧活动栏底部的 **`⚙️ 设置`** 图标（或快捷键 `Ctrl + ,`）；
2. 切换至 **`🤖 模型与凭据`** 选项卡；
3. 点击 **`+ 添加渠道`**，选择驱动提供商（OpenAI / Claude / Gemini / Grok / Azure / Ollama）；
4. 在 **API 密钥 / OAuth JSON 凭据** 框中直接输入：
   - 粘贴单个 `sk-...` 或换行输入多个 Key 轮询；
   - 粘贴 Google ADC / Service Account JSON；
   - 粘贴 new-api OAuth 凭据 JSON 或 Refresh Token；
5. 点击 **`⚡ 测试连通性`**，确认延迟并点亮探活绿灯后保存为默认主渠道。

### 3. 打开项目工作区
1. 点击顶栏活动目录名称，调用系统原生资源管理器拾取工作区文件夹；
2. 左侧侧边栏自动加载文件树（支持 1 层受控异步懒加载，防超大项目卡顿）与 Git 变更状态。

### 4. 智能对话与全自主任务接续
1. **全自主统一 Coding Agent（自然语言驱动）**：
   - 彻底废除割裂的多模式选择器，全面进化为全自主统一 Coding Agent 架构（对齐 Cursor Composer 与 Claude Code 哲学）；
   - **问答与审查**：输入“解释代码”、“查某函数”或“审查代码”，AI 自主通过检索与读取完成分析，绝不擅动文件；
   - **代码修改**：输入“修复 Bug”、“实现某接口”，AI 精准改动并自动带出 Monaco Diff 视窗待审核采纳；
   - **测试驱动**：输入“写测试验证”或“按 TDD 规范”，AI 自主补齐测试用例并级联调用真实测试套件验证；
   - 敲回车直接发起推理，彻底消除模态弹窗与模式切换心智摩擦；
2. **长任务无损接续**：输入「继续」、「continue」或「接着做」，内核直接复用历史既定目标与未完成清单接续执行，不推翻重来；
3. **查看执行过程**：AI 推理过程中的心智思维链（ThinkingBlock）与工具调用（ToolCard）均支持折叠展开，输入区支持 `■ 终止` 即时中断。

### 5. 代码审查与一键采纳
1. AI 修改文件后，工作区自动弹出 Monaco Diff 对比视窗；
2. 审查变更内容：
   - 点击 Hunk 卡片上的 `[✓ 采纳块]` 或 `[✕ 丢弃块]` 进行行级细粒度控制；
   - 点击顶栏 **`全部采纳`** 批量将改动加入 Git 暂存区；
3. 在左侧 Git 抽屉输入 Commit 消息并点击提交。

### 6. 常用快捷键速查

| 快捷键 | 功能描述 |
| :--- | :--- |
| **`Ctrl + \``** | 快速唤起 / 隐藏底部集成终端抽屉 |
| **`Ctrl + ,`** | 快速打开系统设置中枢模态窗 |
| **`Ctrl + K`** | 全局快速启动与功能跳转面板 (可输入 `/hotplug` 直达算子大盘) |
| **`Esc`** | 关闭当前处于顶层的模态弹窗或抽屉 (含算子大盘/设置窗) |

---

## 🛠️ 五、本地开发与构建指南

### 前置依赖
* **Go**：1.22+
* **Node.js**：20+ (带 npm / pnpm)
* **Wails CLI**：v2.9+ (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
* **Git**：2.30+

### 1. 运行微内核测试与架构守卫
```bash
# 运行全部 Go 单元测试
go test ./...

# 运行架构合规守卫脚本 (检查分段锁、Rail 防线与工具注册规范)
go run ./tools/archcheck
```

### 2. 编译前端静态资源
```bash
cd frontend
npm install
npm run build
cd ..
```

### 3. 一键编译桌面程序与打包 Windows 安装向导
```powershell
# 执行 Windows 生产级构建打包脚本
powershell -ExecutionPolicy Bypass -File scripts/build-windows.ps1
```
打包脚本将自动执行架构合规检查、前端 Vite 生产编译、Go 微内核编译并内嵌输出至 `release/` 目录。

---

## 📚 六、核心工程知识沉淀 (docs/knowledge/)

本项目严格遵守知识点沉淀规约，在 `docs/knowledge/` 下归档了 39 篇专注于现行 Wails v2 + Go 微内核 + Vue 3 架构的底层核心机制剖析与实战解决方案：

* [15 - Wails v2 生产级 Desktop 标签编译、Frameless 沉浸式窗体与纯 Go 原生安装向导封装](docs/knowledge/15-wails-v2-production-build-and-frameless-installer.md)
* [16 - Git 行级 Unified Diff 结构化解析、Hunk 分块与单块 Cherry-Pick 采纳/逆向丢弃实现机制](docs/knowledge/16-monaco-unified-diff-and-hunk-cherry-pick.md)
* [17 - Windows CREATE_NO_WINDOW 受控流式终端管道、命令中断与前后端双向事件流设计](docs/knowledge/17-controlled-streaming-terminal-and-no-window-pty.md)
* [18 - MCP 跨进程 Stdio 协议传输、生命周期管理与 ReAct 算子动态调度机制](docs/knowledge/18-mcp-protocol-stdio-lifecycle-and-react-dispatch.md)
* [20 - 跨语言工作区技术栈自适应探测与多轮自主 ReAct 自然收敛自愈状态机](docs/knowledge/20-language-agnostic-stack-detection-and-natural-react-loop.md)
* [24 - 核心系统前十大关键缺陷全域歼灭与桌面微内核工程加固指南](docs/knowledge/24-top-10-critical-bugs-eradication-and-architecture-hardening.md)
* [48 - 控件错名治理、防脱缰智能熔断器与 TDD 结构化失败提取](docs/knowledge/48-control-naming-honesty-and-runaway-circuit-breakers.md)
* [49 - 多协议网关、OAuth 2.0 刷新机制与 new-api 极简单输入框鉴权体验对齐](docs/knowledge/49-multi-protocol-gateway-oauth-refresh-and-newapi-alignment.md)
* [50 - 嵌套子仓库暂存防崩溃、Git Porcelain 路径清洗、工作区列表去重与采纳健壮性闭环](docs/knowledge/50-submodule-diff-defense-empty-repo-stage-guard-and-git-porcelain-hygiene.md)
* [51 - 插件热插拔中心、DSH 算子大盘与微内核动态拓扑一等入口设计](docs/knowledge/51-hotplug-plugin-center-and-dsh-operator-dashboard.md)
* [52 - 从三态互斥到全自主统一 Coding Agent 架构演进](docs/knowledge/52-unified-autonomous-coding-agent-architecture.md)
* [53 - 版本号校准为 0.0.1 与开发测试孵化阶段工程基线](docs/knowledge/53-version-recalibration-to-early-incubator-phase.md)

完整 39 篇现行文档目录索引请参阅 [`docs/README.md`](docs/README.md) 与 [`docs/knowledge/README.md`](docs/knowledge/README.md)。

---

## ⚖️ 开源协议

本项目采用 MIT 协议开源。
