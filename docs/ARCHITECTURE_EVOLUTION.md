# 湉码 / tiancode 架构演进与技术迭代史 (Architecture Evolution & Engineering Changelog)

> 本文档完整收录 湉码 (tiancode) 历经 56 次核心架构演进、技术攻坚、安全加固与体验治理的详实工程记录。从最初的双架构探索，到最终收敛为 **Wails v2 + Go 原生微内核 + Vue 3** 统一生产栈，全景展现了工作台在自主智能体回路、多协议网关、GitOps 审查、进程生命周期控制与零假数据治理上的技术深度。

---

## 目录索引

- [一、基础架构与核心设计哲学 (迭代 1 - 8)](#一基础架构与核心设计哲学-迭代-1---8)
  - 1. 单一执行内核收敛 (Single Execution Engine)
  - 2. 四维人话收尾机制 (Human-Readable Endings)
  - 3. Session 最小任务模型与「继续」接续 (Session Task Model)
  - 4. 审查任务「先检索/先地图再下钻」与工作区搜索算子 (Search & Map-First Strategy)
  - 5. 文件修改带出 Diff、多页签与人工采纳闭环 (Pending Diff Acceptance & Tabs)
  - 6. 真实双栈 TDD 自动化验证与结构化失败清单 (Dual-Stack TDD & Failure Extractor)
  - 7. 控件正名诚实性与防误触安全交互 (Control Naming Honesty & Safe UI)
  - 8. 纯净零数据初始状态与配置原子写 (Zero Demo & Atomic Store)
- [二、桌面端融合与通信管道 (迭代 9 - 17)](#二桌面端融合与通信管道-迭代-9---17)
  - 9. 生产级前后端基础工程框架与原生双轨上游驱动
  - 10. 全链路推理流可控中断与进程树生命周期隔离
  - 11. 物理受控沙箱、Git 管道秒级快照与物理 Git 暂存中枢
  - 12. ReAct 双环自主执行引擎与动态工具调用卡片
  - 13. Monaco 现代代码工作台与万行 Diff 虚拟化审查视图
  - 14. 系统全局设置中心与多服务商凭据热切
  - 15. 受控集成终端与静默流式 Shell 管道
  - 16. [历史原型归档] React 原型阶段交互与设计沉淀
  - 17. Wails v2 + Go 1.22 纯原生桌面架构与单文件安装向导
- [三、工具协议、编译器自愈与跨语言感知 (迭代 28 - 30)](#三工具协议编译器自愈与跨语言感知-迭代-28---30)
  - 28. MCP 跨进程 Stdio 工具注册与 ReAct 动态自主调度
  - 29. LSP 编译器语法诊断自愈守卫与 MCP 服务治理看板
  - 30. 跨语言工程技术栈自适应探测与多轮自主 ReAct 自然收敛自愈状态机
- [四、缺陷清零、安全沙箱与进程生命周期治理 (迭代 31 - 42)](#四缺陷清零安全沙箱与进程生命周期治理-迭代-31---42)
  - 31. 全域关键缺陷治理与桌面微内核工程加固
  - 32. 进程树生命周期隔离、Untracked Diff 适配与全模态窗完整性治理
  - 33. 文件树与会话防穿越守卫、编译诊断无网络阻断与前端状态洁净性
  - 34. 动态工作区热切换、稀疏工具调用治理与脱机卸载自毁
  - 35. MCP 外部握手并发优化、守护协程防泄漏、Git 空格路径防御与时间戳规范化
  - 36. 插件分段锁原子热替换、Windows 盘符归一化与并发任务取消保护
  - 37. MCP 悬挂通道空指针防御、代码审计 OOM 熔断与 HTTP 连接池复用治理
  - 38. 未追踪代码块安全丢弃、Windows 盘符全链路归一化与网络探活连接池治理
  - 39. 进程树主动取消机制、工具调用 ID 协议守卫与 Git 变更纯净状态治理
  - 40. Git 源码管理全交互闭环、TDD 进程树熔断、文件物理删除 Diff 容错与卸载器自删除锁治理
  - 41. Git 分支检出双横杠陷阱、MCP 管道防死锁排水与流式中断隔离治理
  - 42. 会话更新时序保序、Windows 设备保留名防御与零假凭据架构防线
- [五、Git 管道鲁棒性、无 HEAD 容错与微内核防崩 (迭代 43 - 47)](#五git-管道鲁棒性无-head-容错与微内核防崩-迭代-43---47)
  - 43. 工具空输出防御、RevertFile 撤销防误删、真实快照时间戳与事件全量清理
  - 44. 目录防覆盖防御、Git 暂存区感知提交、MCP 锁粒度优化与全链路无黑框防护
  - 45. Git Porcelain v2 重命名解析修复、无 HEAD 初始仓库安全撤回、MCP 并发 I/O 锁优化与暂存区前端闭环
  - 46. 无 HEAD 仓库 AM 状态 Diff 修正、安全审计大小写双向归一化、取消暂存平滑降级与跨平台进程自愈
  - 47. HTTP 引擎空指针熔断、Git 分支全字符注入防御、已删除文件 Diff 状态机与 MCP 插件空守卫
- [六、工作区动态绑定、双栈 TDD 与自主交付收敛 (迭代 48 - 52)](#六工作区动态绑定双栈-tdd-与自主交付收敛-迭代-48---52)
  - 48. 工作区动态重绑定、Tab 内存持久化、TDD 双栈闭环与文件树按需懒加载
  - 49. 开发者工作区全真检索、策略跳过守卫、实验特性诚实标注与 V1 边界矩阵对齐
  - 50. 剥离形式主义外壳、主界面五大极简入口与设置中枢真实化治理
  - 51. 终结固定轮次硬限制、确立 AI 自主判断任务结束与防爆安全兜底机制
  - 52. 嵌套子仓库暂存防崩溃、Git Porcelain 路径清洗、工作区列表去重与采纳健壮性闭环
- [七、插件热插拔大盘、统一全自主 Agent 与现代架构治理 (迭代 53 - 56)](#七插件热插拔大盘统一全自主-agent-与现代架构治理-迭代-53---56)
  - 53. 插件热插拔中心、DSH 算子大盘与微内核动态拓扑一等入口设计
  - 54. 废除三态互斥模式与进化为全自主统一 Coding Agent
  - 55. 版本号统一定标为 0.0.1 与开发测试孵化阶段工程基线
  - 56. 重构升级代码架构与依赖治理工作板 (分层拓扑、契约矩阵与影响面雷达)

---

## 一、基础架构与核心设计哲学 (迭代 1 - 8)

### 1. 单一执行内核收敛 (Single Execution Engine)
* **消除双核隐患**：彻底合并清理冗余的第二套循环代码，所有大模型推理与工具调用全部收敛至直连内核；
* **AI 自主终态判定与防脱缰双重智能熔断器**：
  * **自主交付闭环**：废止固定轮次硬上限强制截断，当大模型不再调用任何工具时，即代表自主交付完成；
  * **20 轮防爆兜底**：设立 20 轮硬上限作为极端安全保险丝，杜绝无节制死循环；
  * **重复调用主动熔断**：追踪连续工具调用指纹，当模型连续 3 次发起完全相同的工具调用时，立即触发硬熔断并引导总结，防止死循环空耗 Token；
  * **连续错误主动熔断**：当工具执行连续报错 3 次时，立即触发连续失败熔断并向用户汇报排查进展，避免冲破上下文窗口。

### 2. 四维人话收尾机制 (Human-Readable Endings)
* **告别「突然没了」**：系统在任何非正常终止或边界场景下，均以通俗易懂的人话呈现结构化说明与下一步建议：
  * **触顶与熔断收敛 (Capped / Breaker)**：总结当前阶段已探明成果与未完成项，并提示用户下一步排查或接续建议；
  * **用户中止 (Interrupted)**：保存已产生的所有结果与上下文，输出中止人话提示，前端恢复就绪；
  * **上游网关报错 (HTTP 400/401/429/500)**：自动解析上游状态码，指出根本原因（上下文击穿/鉴权失效/频率超限/服务宕机）并给出排查建议；
  * **模型空回复 (Empty)**：捕获 0 内容且无工具调用的异常空包，输出防御说明，指导换模型或重试。

### 3. Session 最小任务模型与「继续」接续 (Session Task Model)
* **结构化状态挂载**：在 `ChatSession` 上挂载 `TaskModel`，承载 `Goal`、`Status`、`ToolBudget`、`ToolsUsed`、`Summary`、`PendingDiffFiles` 与 `TDDPassed`；
* **「继续」精准接续**：当用户输入「继续」、「continue」、「接着做」时，内核直接读取当前会话的历史既定目标与未完成项注入上下文，禁止推翻重来或重复做顶层勘探。

### 4. 审查任务「先检索/先地图再下钻」与工作区搜索算子 (Search & Map-First Strategy)
* **一等工作区检索算子 (`tool.search`)**：实现原生 Go 高性能沙箱检索插件 `search_workspace`，提供 `grep`（文本/代码模式匹配）与 `find`（文件名通配），并在 System Prompt 强制「先检索与先地图再下钻」，彻底终结大模型无脑递归 list 扫盘；
* **审查硬闸白名单**：在 `StrategyAnalyze` 第 1 轮严格仅放行根目录清单文件（`go.mod`、`package.json`、`Cargo.toml`、`README` 等）与顶层文档，硬拦截所有根源码与深层文件。

### 5. 文件修改带出 Diff、多页签与人工采纳闭环 (Pending Diff Acceptance & Tabs)
* **改文件默认出 Diff**：智能体写入代码时自动触发 `agent:files_changed`，前端自动激活 Monaco Diff 工作区并定位聚焦该文件差异；
* **Monaco 多文件标签页 (Tabs)**：支持多文件同时打开、标签切换、关闭及未保存脏标记联动；
* **人点接受才算完成与发送强阻断**：修改文件进入 `PendingDiffFiles` 队列，顶栏实时显示脉冲徽章；若存在未确认改动，发送下一句新提示时自动弹出居中拦截弹窗，提示开发者优先确认或放弃修改，杜绝盲目覆盖。

### 6. 真实双栈 TDD 自动化验证与结构化失败清单 (Dual-Stack TDD & Failure Extractor)
* **拒绝假成功与无测试直接失败**：`RunTDDValidation` 支持自动探测 Go 原生测试 (`go test -v ./...`) 与 Node 前端测试 (`npm test`)；无测试直接报失败；
* **结构化失败用例精准解析**：自动解析 Go（`--- FAIL: TestFoo`）与 Node/Vitest/Jest（`FAIL file`, `✕ test`, `✖ test`, `● test`）失败用例名，生成 `FailedTests []string` 清单，并在输出和 Toast 中置顶显式高亮，开发者一眼即明无需人肉 grep 日志。

### 7. 控件正名诚实性与防误触安全交互 (Control Naming Honesty & Safe UI)
* **诚实正名**：
  * 将原「微内核快照」诚实正名为「Git 暂存储藏 (Stash)」，操作如实对应 `＋ 储藏 (Stash)` / `恢复 (Pop)` / `无暂存记录`；
  * 将原「代码工作区」正名为「文件与编辑器」；
  * 将原「检索分支、文件与算子」正名为「快捷跳转文件、会话或面板...」；
* **真实模型健康状态灯**：顶栏指示灯严格绑定主渠道真实探活状态（在线绿色带真实网络往返延迟、待测速琥珀色、离线红色、未配置灰色、推理中橙色脉冲），彻底消除常亮假绿灯；
* **策略切换原生安全下拉框**：废止盲目 1-click 循环切换，采用原生选项下拉框，彻底消除误触切换到 `implement`（全写权限放行）的隐患。

### 8. 纯净零数据初始状态与配置原子写 (Zero Demo, Clean Empty State & Atomic Store)
* **根绝所有隐含假数据**：彻底清理系统首次启动对 MCP/Skill/Rule 默认预埋伪数据的潜载逻辑，所有未配置项默认均严格为纯净空切片 `make([]T, 0)`；
* **事务级原子配置持久化 (`atomicWriteConfig`)**：摒弃直接裸写 `os.WriteFile` 的竞态隐患，全面采用“临时文件写入 + 磁盘同步 + 原子重命名”的写入隔离机制，杜绝 Windows 下多协程并发写入导致的文件锁冲突与 JSON 撕裂。

---

## 二、桌面端融合与通信管道 (迭代 9 - 17)

### 9. 生产级前后端基础工程框架与原生双轨上游驱动 (Base Framework & Dual-Track Upstream Drivers)
* **后端 Go 插件式微内核 (`backend/`)**：
  * **强类型 SPI 契约 (`pkg/plugin/v1/`)**：规范 `Plugin`、`ProviderPlugin` (带背压通道)、`ToolPlugin` (入参 Schema 校验)、`RailPlugin` (多阶段拦截与阻断)；
  * **分段锁注册中心 (`internal/host/registry.go`)**：无全局大锁，支持高并发读取与按优先级降序调度；
  * **Panic 隔离看门狗 (`internal/host/guard.go`)**：结合 `recover()` 与 `debug.Stack()` 捕获插件堆栈，保障微内核常驻守护进程 100% 高可用；
  * **OpenAI 与 Claude 双轨原生上游驱动**：中立规范层 (`pkg/protocol/canonical.go`) 抹平协议差异，原生支持 OpenAI Chat Completions 协议族（GPT-4o、DeepSeek、SiliconFlow）与 Anthropic Claude Messages API 官方协议（支持 Claude 3.7 Thinking 深度思考流与 Prompt Caching 用量审计）。
* **前端 Vue 3 现代工程 (`frontend/`)**：
  * 基于 **Vue 3 + TypeScript 5.5 (Strict) + Vite 6 + Tailwind CSS v4**；
  * 完整落地 Warm Minimalist 调色盘（`#FAF8F5` / `#F4EFEA` / `#D96B27`）与 16:9 人机工学单焦点视口布局；
  * 顶栏单焦点切换胶囊（`💬 对话` / `◫ 双栏协同` / `📝 代码区`）、48px 侧边活动栏、生产级双层 Git 源码控制面板与集成终端抽屉（`Ctrl + \`` 全局快捷唤起）。

### 10. 全链路推理流可控中断与进程树生命周期隔离 (Controllable Streaming & Process Tree Isolation)
* **前端至底层无阻塞中断闭环 (`CancelAgentStream`)**：
  * 前端输入区流式推理时自动呈现高亮脉冲的中断按钮 `■`（支持快捷键或一键点击）；
  * Wails IPC 瞬态派发 `CancelAgentStream` 信号，后端原子取消 `agentCancel` 上下文，实时切断正在进行的大模型 SSE 网络请求与长循环；
  * 同步广播 `agent:interrupted` 事件并记录当前轮已生成的思考与文本进度，优雅退回就绪态。
* **Windows 孤儿子进程树递归强杀守卫**：
  * 在受控沙箱与终端工具的同步/流式执行层，统一注入 `execCtx.Done()` 守护协程；
  * 借助 `taskkill /F /T /PID <pid>` 深度强杀顶层 `cmd.exe` 下属整棵子进程树，彻底根除后台孤儿 node/test 进程挂死与 CPU 耗尽风险。
* **Windows 原生单文件安装向导与异步自删除卸载器**：
  * 单文件安装程序支持图形交互与 `/S` / `--silent-install-dir` 纯静默无头安装；
  * 卸载器基于独立临时批处理脚本实现无残留异步延时文件解绑与目录自删除，全链路无控制台弹窗闪烁（`0x08000000`）。
* **前端流式解码与人机交互 (`frontend/src/`)**：
  * **流式客户端 (`core/transport/sseClient.ts`)**：基于 `ReadableStream` 逐帧解析，解决 UTF-8 多字节分片乱码；
  * **深度思考折叠卡片 (`app/chat/ThinkingBlock.tsx`)**：思考中呼吸指示灯 + 思考完毕紧凑折叠，支持随时展开查阅思维链；
  * **平滑打字机上屏**：助手回复平滑逐字增量流式渲染，附带打字防抖与自动平滑滚动。

### 11. 物理受控沙箱、Git 管道秒级快照与物理 Git 暂存中枢 (Controlled FS Sandbox & Plumbing Snapshots)
* **物理文件受控沙箱 (`backend/internal/core/sandbox/fs.go`)**：
  * **原子写防撕裂机制**：写入同级临时文件 ➔ `file.Sync()` 强制落盘 ➔ `os.Rename` 原子覆盖，从物理层杜绝因断电、崩溃造成的源文件撕裂损坏；
  * **路径沙箱严格防御**：获取真实工作区绝对路径并执行 `filepath.Clean`，严禁任何越界跨盘符或目录穿越（如 `../../etc/passwd`）攻击。
* **Git Plumbing 底层管道秒级影子快照 (`backend/internal/core/sandbox/snapshot.go`)**：
  * **零分支污染**：放弃传统的 `git commit`，直接调用 Git 底层管道：`git write-tree` 生成树对象 ➔ `git commit-tree` 生成孤立提交；
  * **毫秒级安全锚点**：记录于 `.git/refs/tcode/snapshots/` 命名空间下，Agent 修改任何代码前 $\le 5\text{ms}$ 自动生成快照，支持一键无损秒级回退。
* **真实双层 Git 暂存控制面板 (`frontend/src/app/git/GitPanel.tsx` & `core/store/gitStore.ts`)**：
  * 解析 `git status --porcelain=v2`，精准呈现已暂存（Staged）与未暂存（Working）文件列表；
  * 支持一键单文件暂存（`+`）、取消暂存（`-`）、撤销放弃更改（`↺`）与带状态感知的 Commit 提交。

### 12. ReAct 双环自主执行引擎与动态工具调用卡片 (ReAct Autonomous Engine & ToolCard)
* **微内核 ReAct 自主执行状态机 (`backend/internal/core/loop/engine.go`)**：
  * **自主工具发现与声明**：自动扫描并收集注册中心内的全部 `ToolPlugin` 动态注入 OpenAI 规范的 `tools` 契约；
  * **流式切片拼接器 (`ToolReassembler`)**：增量聚合上游模型切碎的 `tool_calls` 参数分片，防止 JSON 截断畸形；
  * **多轮自愈循环 (Inner Loop)**：模型调用工具 ➔ 本地沙箱执行 ➔ 结果转换为 `role: tool` 回传上下文 ➔ 再次推理，支持最多 15 步防死循环熔断保护。
* **微内核多阶段 SSE 协议传输 (`backend/internal/transport/http/server.go`)**：
  * 扩展标准事件：`chunk` (正文与思考增量)、`tool_start` (工具启动与入参)、`tool_end` (执行结果与成功状态)、`done` (任务收敛完成)。
* **前端工具调用卡片 (`frontend/src/app/chat/ToolCard.tsx`)**：
  * 遵循 Warm Minimalist 暖色极简设计：工作台米灰（`#F4EFEA`）外框、运行中陶土暖橙（`#D96B27`）呼吸光晕、成功绿色指示；
  * 默认 32px 紧凑收起，点击平滑展开抽屉，内嵌代码暖黑（`#1E1C1A`）小代码块实时查阅入参 JSON 与返回输出；
  * 与 `ThinkingBlock`（思考卡片）和打字机气泡按严格时序编排渲染。

### 13. Monaco 现代代码工作台与万行 Diff 虚拟化审查视图 (Monaco Editor & Dual-Column Diff Reviewer)
* **真实文件资源管理器与多 Tab 标签页 (`frontend/src/app/editor/`)**：
  * **树形工作区目录 (`FileTree.tsx`)**：递归树形结构，智能过滤 `.git`, `node_modules` 等开发中间层，按后缀自适应映射语言图标；
  * **多 Tab 标签栏 (`TabBar.tsx`)**：纯白底色配顶部陶土暖橙强调线，支持多文件自由切换、一键保存与悬停关闭；
* **Monaco 现代代码编辑器与 Diff 审查模式 (`EditorWorkspace.tsx`)**：
  * 深度定制 Warm Minimalist 调色盘，适配 JetBrains Mono / Fira Code 等宽字体，代码行号、折叠与语法高亮完备；
  * **万行 Diff 虚拟化审查 (Diff Reviewer)**：一键切入 Monaco `DiffEditor` 双栏对比，左侧基准（Git HEAD 原始快照）与右侧最新改动红绿对照，右上角悬浮工具条提供一键放弃更改（`Restore`）与一键暂存（`Stage`）；
  * **行级 Hunk 状态机分块与单块 Cherry-Pick**：右侧审查区通过 Go 微内核将 Diff 结构化切分为独立变更块（Hunk），直观呈现 `块 #N` 增删行数统计；每个 Hunk 配备独立的 `[✓ 采纳块]`（`git apply --cached` 暂存入 Index）与 `[✕ 丢弃块]`（`git apply --reverse` 局部无损反向还原），并即时与左侧 Git 抽屉联动刷新，实现极致的细粒度代码控制。
* **顶栏单焦点工作台联动**：
  * 完美联动 `💬 对话`、`◫ 双栏协同`、`📝 代码模式`，实现从对话思维链到代码编辑的沉浸式无缝流转。

### 14. 系统全局设置中心与多服务商凭据热切 (Settings Modal & Multi-Provider Hot-Swapping)
* **严格居中弹窗三维铁律 (`frontend/src/app/settings/SettingsModal.tsx`)**：
  * **绝对水平垂直居中**：无论窗口尺度缩放，弹窗在视口正中精准吸附，搭配极简半透明毛玻璃遮罩；
  * **退出双重保障**：键盘全局 `Esc` 键层级化阻断退出 + 外部背景遮罩点击退出 + 右上角显式 `[X]` 关闭按钮与 Tooltip；
  * **弹窗三标签页分类**：
    * `🤖 模型与凭据 (Providers)`：可视化配置 Base URL、API Key、默认模型与 Thinking 深度思维链开关；
    * `🛡️ 沙箱与安全 (Sandbox)`：管控原子写落盘防撕裂与写前自动 Git 影子快照；
    * `📝 系统指令提示词 (System Prompts)`：自定义注入大模型的全局规则与代码生成风格；
* **一键物理连通性打点测速探针 (`POST /api/config/ping`)**：
  * 点击「测试网络连通性」，即时向上游网关发起毫秒级真实探活请求，呈现 TTFT 延迟与连通性绿标；
* **持久化安全状态机 (`core/store/settingsStore.ts`)**：
  * 自动无感持久化至 `localStorage`，支持全局快捷键 `Ctrl + ,` 或侧边栏齿轮一键唤起。

### 15. 受控集成终端与静默流式 Shell 管道 (Controlled Streaming Terminal & Silent Shell Executor)
* **底部集成式可折叠终端抽屉 (`frontend/src/App.vue` & `core/wailsBridge.ts`)**：
  * 深度定制 Warm Dark 暖炭黑界面（`#161412` 底色、`#F4F4F5` 字体、`#D96B27` 陶土暖橙高亮与提示符）；
  * 全局快捷键 **`Ctrl + \``**（或顶栏/活动栏终端按钮）瞬间唤起/隐藏抽屉，支持 `🗖` 高度平滑切换与清屏；
  * 双 Tab 架构：`$_ 终端控制台`（命令行交互与命令历史上下键回溯）+ `Agent 执行链路`（SSE 事件流实时 Trace）；
* **Windows 平台零弹窗与双通道流式管道 (`plugins/tool/terminal/terminal_tool.go` & `app.go`)**：
  * 后端启动子进程时严格注入 `CREATE_NO_WINDOW = 0x08000000` 与 `HideWindow: true`，**彻底杜绝任何 Windows CMD 黑色黑框弹出打扰用户**；
  * 基于 `StdoutPipe` 与 `StderrPipe` 双 Goroutine 并发读取，通过 Wails 事件系统（`terminal:data`）毫秒级打字机流式推送到前端，长命令无需等待全量阻塞；
  * 基于 `context.WithCancel` 支持用户一键 `[■ 终止]` 取消正在执行的进程，杜绝孤儿进程与句柄泄漏；
  * 执行目录物理锁定于工作区根目录，具备安全防穿越防护。

### 16. [历史原型归档] React 原型阶段交互与设计沉淀 (Historical Prototype Notes)
> ⚠️ **历史归档声明**：早期原型阶段（WP 16~26）在 React / Python 环境下的探索记录已完整迁出归档至 [`docs/archive/HISTORICAL_PROTOTYPE_NOTES.md`](archive/HISTORICAL_PROTOTYPE_NOTES.md)。
> 当前发货代码与日常施工请 100% 严格遵循现行 **Wails v2 原生桌面端 + Go 微内核 + Vue 3** 架构。

### 17. Wails v2 + Go 1.22 纯原生桌面架构与单文件安装向导 (Wails Native Architecture & Standalone Go Installer)
* **Wails v2 生产级条件标签编译体系**：
  * 基于 Go 1.22 + Wails v2 + Vue 3.4 纯原生架构，采用 `-tags "desktop,production"` 彻底打通 Windows Edge WebView2 深度融合，去除空壳 Stub 回退，杜绝任何启动红叉异常；
  * 二进制裁剪采用 `-ldflags="-H windowsgui -s -w"`，剥离符号表与调试元信息，二进制体积直降 35%（~9.6MB），且彻底消除了任何控制台 CMD 黑色闪烁黑框；
* **纯 Go 嵌入式单文件自解压安装向导 (`bin/湉码Studio_Setup_v2.0.0.exe`)**：
  * 基于 `//go:embed` 深度内嵌主桌面程序 `tiancode.exe` 与独立卸载器 `uninstall.exe`，免除外部打包器依赖；
  * Win32 原生 API `MessageBoxW` 提供高亲和力安装向导交互；支持 `-silent` 极速静默安装；
  * 遵循现代桌面软件工程规范，自动部署至 `%LOCALAPPDATA%\Programs\湉码Studio`（无需 UAC 提权干扰），全自动创建桌面与开始菜单快捷方式，并完整写入 Windows 注册表卸载中心；
* **铁律 1.5 物理闭环自动化验证体系**：
  * 物理安装验证 ➔ 真实进程生命周期探活 ➔ AgentRouter WAF 穿透流式推理 ➔ 真实工作区工具调用端到端回归，全链路保障高可靠交付。

---

## 三、工具协议、编译器自愈与跨语言感知 (迭代 28 - 30)

### 28. MCP 跨进程 Stdio 工具注册与 ReAct 动态自主调度 (MCP Protocol Stdio & ReAct Dispatch Loop)
* **Anthropic MCP 标准协议支持**：
  * 基于标准输入输出（Stdio）管道与 JSON-RPC 2.0 报文，无缝支持第三方生态（如 `@modelcontextprotocol/server-filesystem`、PostgreSQL、GitHub API 等）的免侵入式挂载；
  * 实现完整的初始化握手协议（`initialize` ➔ `notifications/initialized`）、实时探活（`ping`）与工具动态探测（`tools/list`）；
* **Windows 零黑框外部进程管控 (`0x08000000`)**：
  * 在拉起任何外部 Node/Python 工具服务进程时，强制通过 `SysProcAttr.CreationFlags` 注入 `CREATE_NO_WINDOW = 0x08000000` 与 `HideWindow: true`，杜绝任何黑色 CMD 控制台窗口弹出打扰；
* **Manager 算子全局路由树与 ReAct 调度闭环**：
  * 管理器在内存中并发安全维护 `clients` 与 `toolRouting` 映射，启动时根据配置自动拉起已启用的服务；
  * 在大模型发起第一轮流式推理前，将本地沙箱工具（`exec_command`, `write_file`, `read_file`, `git_status`）与所有在线 MCP 算子合并生成工具集；
  * 当模型发起算子调用时，微内核在 $O(1)$ 时间内精准路由派发到对应子进程，执行后结果返回 ReAct 上下文驱动后续自愈推理，完成全自主端到端闭环。

### 29. LSP 编译器语法诊断自愈守卫与 MCP 服务治理看板 (LSP Diagnostics & MCP Management)
* **轻量级多语言编译器语法探针**：
  - 原生支持 Go (`go vet`)、TypeScript/JavaScript (`npx tsc --noEmit`) 与 Python (`python -m py_compile`) 毫秒级轻量静态检查；
  - 探针运行全程注入 Windows 零黑框标志位（`CREATE_NO_WINDOW = 0x08000000`），超时 4 秒严格熔断，不侵入主交互线程；
* **落盘即诊断与 ReAct 智能体自愈回路 (Self-Healing Loop)**：
  - 当大模型触发 `write_file` 动作原子落盘代码后，Go 微内核自动运行编译器诊断；若捕获未定义标识符、缺少 import 或语法红线，自动置顶追加至工具返回结果中，驱动大模型在下一轮循环中自动修复消除编译错误；
* **前端 MCP 运维可视化看板 (`SettingsModal.vue`)**：
  - 采用 Warm Minimalist 极简卡片，实时呈现服务在线状态指示灯、毫秒级握手延迟与算子数量；
  - 支持一键「⚡ 测速探活」，点击「查看算子 ▼」可平滑展开探测到的工具列表（名称与参数说明）；
  - 提供表单抽屉支持即时添加、编辑、持久化删除（`DeleteMCP`）以及一键启停服务。

### 30. 跨语言工程技术栈自适应探测与多轮自主 ReAct 自然收敛自愈状态机 (Language-Agnostic Stack Detection & Multi-Turn Autonomous Loop)
* **跨语言工程技术栈轻量探测 (`internal/core/sandbox/env_detect.go`)**：
  - 自动扫描工作区标记文件（`package.json`, `Cargo.toml`, `go.mod`, `pyproject.toml`, `pom.xml`），毫秒级提取主语言、框架、构建工具与建议测试命令；
  - 动态注入 System Prompt 环境感知块，彻底消除硬编码单一语言偏见，让大模型自适应采取匹配的验证手段；
* **Zero Tool Calls 自然收敛多轮自愈状态机 (`app.go`)**：
  - 以“大模型判定目标已达成或无工具下发（Zero Tool Calls）”作为核心自然终止准则，摒弃机械的人工轮数限制；
  - 形成自动化闭环：`物理写盘 ➔ LSP 语法诊断 ➔ 注入报错 ➔ 自愈修复 ➔ 自主执行验证 ➔ 目标达成收敛交付`；
* **纯净零假数据与真实空状态架构 (Zero Demo & Clean Empty State)**：
  - 彻底清理前端、状态机与 IPC 桥接层所有硬编码的演示数据（假气泡、假思考、假工具调用、假 Diff 变更、预填 API Key、假终端命令）；
  - 全域落地纯净空状态（Clean Empty State）：初次打开、未连上游或无会话时真实暴露空提示与引导，严格 Fail-Closed 杜绝虚假繁荣；
* **LRU Map Markdown 渲染缓存与微触感交互响应**：
  - 引入 LRU 内存缓存池，彻底根除模板内联调用 `renderMarkdown` 导致的 DOM 全量重绘 CPU 抢占（Render Cascade）；
  - 弹窗与选项卡切换全面解耦阻塞性数据扫描，0ms 瞬间唤起并搭配优雅加载指示器；
  - 注入 `:active` 物理微触感反馈，全面提升交互操作响应速度与顺滑度。

---

## 四、缺陷清零、安全沙箱与进程生命周期治理 (迭代 31 - 42)

### 31. 全域关键缺陷治理与桌面微内核工程加固 (Top 10 Critical Bug Eradication & Hardening)
* **凭据硬编码彻底拔除与 Fail-Closed 防御**：
  - 彻底清理后端配置中心与对话推理流中任何硬编码的测试 Token 与假模型 `deepseek-v4-flash`；
  - 遵循严格的 Fail-Closed 原则：当未配置模型渠道 API Key 时，立即向前端抛出结构化配置指引并终止推理，严禁静默回退；
* **OpenAI / DeepSeek 协议合规加固 (`Message.Content`)**：
  - 针对工具消息 (`role: "tool"`)，移除 Go 结构体 `Content` 字段的 `omitempty` 标签，确保即便工具执行标准输出为空字符串也严格序列化传输 `"content": ""`，100% 消除上游网关 400 Bad Request 校验拒绝；
* **Wails IPC 全局事件监听器泄漏根治**：
  - 在 `wailsBridge.sendMessage` 中引入自动化解绑生命周期守护，在流式连接建立前与完成时调用 `runtime.EventsOff` 注销全局总线监听器，根治多轮会话导致的 Chunk 翻倍打印与内存雪崩；
* **深层递归文件树与沉浸式无边框窗体控制**：
  - 重构 `GetFileTree` 为支持 4 层深度递归扫描并自动过滤构建依赖目录（`.git`, `node_modules`, `bin`, `dist` 等）；
  - 标题栏注入 `--wails-draggable:drag` 原生拖拽，右上角无边框控制按钮完整打通最小化、最大化/还原与安全退出；
* **安装包自定义路径隔离与卸载器全盘防误杀守卫**：
  - 单文件安装程序全面支持 `--dir=`, `-dir=`, `/D=`, `--silent-install-dir=`，自动对自定义目录校验并补齐 `湉码Studio` 隔离子目录；
  - 卸载程序引入物理路径多重安全断言（禁止系统根目录、禁止非 `湉码Studio` 目录整体 `rmdir`），并采用 `CREATE_NO_WINDOW` 隐蔽控制台执行自清理。

### 32. 进程树生命周期隔离、Untracked Diff 适配与全模态窗完整性治理 (Process Tree Isolation & UI Modal Completeness)
* **Windows 孤儿进程树安全治理 (`KillProcessTree`)**：
  - 针对外部 MCP Stdio 协议与流式长耗时命令，在用户中止或上下文超时时，注入 `CREATE_NO_WINDOW`（`0x08000000`）执行 `taskkill /F /T /PID <pid>` 强杀完整子进程树，彻底根除 Windows 控制台黑框弹窗与管道读写永久锁死挂死；
* **Git Porcelain 全维度差异与 Untracked 物理撤销**：
  - 打破传统 `git diff` 无法计算工作区未追踪（Untracked）新文件的盲区，结合 `git status --porcelain` 自动识别 `??` 与 `A ` 文件并生成行级全量绿字新增块（`+N 行`）；
  - `RevertFile` 在 `git checkout` 失败时智能回退物理安全清理（`os.Remove`），杜绝撤销新文件时的崩溃；
* **沙箱盘符大小写归一化与原子写抗争机制**：
  - 对 Windows 盘符进行小写归一化规约，杜绝跨大小写（如 `d:` vs `D:`）导致的路径越界误报拦截；并在目标文件被外部读取句柄锁定时提供覆写重试与容灾回退；
* **多轮自主 ReAct 算子执行全时序链路持久化**：
  - 在会话持久化实体中扩展 `Tools []ToolExecution` 切片，支持单条消息中多次算子调用的完整持久化存储与时序动态展开；
* **前端全模态窗闭环与人机工学规约补齐**：
  - 完整补齐 MCP 导入、Agent 技能创建、工程规约新增等全套居中暖色模态窗，提供显式 `[X]`、`Esc` 退出与键盘监听；
  - 补齐 AST 架构拓扑实体至主输入框的一键引用注入（`injectNodeToPrompt`）与代码变更真实物理暂存（`stageFileAction`）。

### 33. 文件树与会话防穿越守卫、编译诊断无网络阻断与前端状态洁净性 (Path Traversal Defense & Session Hygiene)
* **会话 ID 白名单清洗与越界删除安全守卫**：
  - `Store.Get` / `Save` / `Delete` 引入 `sanitizeID` 白名单校验，彻底阻断利用 `../../` 遍历、覆盖或删除会话目录外任意物理文件的路径穿越攻击；
  - 根除遗留硬编码默认标签 `核心架构`，遵循数据洁净原则设为 `默认`；
* **文件树受控沙箱绝对路径校验与 500 节点防环熔断**：
  - `GetFileTree` 与 `buildFileTree` 强制经由受控沙箱 `ValidatePath` 校验并利用 `EvalSymlinks` 阻断软链接循环递归，设定 500 节点全局上限，杜绝超大目录导致客户端 DOM 渲染卡死；
* **文件回滚物理删除沙箱校验隔离 (`RevertFile`)**：
  - 严格校验相对路径是否越出工作区，彻底消除 `git checkout` 失败回退删除未追踪文件时的越权任意文件删除漏洞；
* **会话数据原子写隔离 (`atomicWriteSession`)**：
  - 会话磁盘存储全量采用“临时文件写入 + 磁盘同步 + 原子重命名”机制，杜绝断电或高频流式交互时的 JSON 数据撕裂与空文件损坏；
* **大模型用量遥测成本精准核算**：
  - 彻底铲除遥测模块中误用 `time.Now().Format` 导致成本输出系统时间的 Bug，基于 Token 吞吐真实核算并格式化精准美元开销；
* **Windows 全链路外部进程零黑框防护规约**：
  - 全局封装 `windowsSysProcAttr()`，为微内核调用的所有 Git、Npx、Python 进程强制注入 `0x08000000` (`CREATE_NO_WINDOW`) 与 `HideWindow: true`，杜绝一切黑框弹窗闪烁；
* **JSON-RPC ID 宽容解析与非阻塞投递**：
  - MCP Stdio 客户端完整支持 `string`、`int`、`float64` 与 `json.Number` 多类型 Request ID 解析，并在 channel 投递处增加 non-blocking `select` 保护，防止 reader 协程永久挂起；
* **流式生成中删除会话防幽灵复活**：
  - 前端删除当前正在生成的会话前先主动触发 `stopGenerationAction()` 中断底层流式上下文，杜绝异步落盘导致已删除会话在磁盘上重新复活。

### 34. 动态工作区热切换、稀疏工具调用治理与脱机卸载自毁 (Workspace Hot-Swap, Sparse Tool Indexing & Detached Uninstaller)
* **动态工作区热切换与原生目录拾取器 (`OpenDirectoryDialog` & `SetWorkspace`)**：
  - 彻底铲除顶栏项目名硬编码 `agent-learning` 缺陷，标题栏与工程资源管理器同步展示当前活动目录，支持点击调起系统原生文件管理器原生拾取文件夹（`runtime.OpenDirectoryDialog`，符合铁律 5）；
  - 微内核实现 `SetWorkspace` 动态热更新机制，无缝重载受控沙箱、快照管理器、Git 状态检测、受控终端与 MCP 管理进程，并即时联动刷新前端文件树与 AST 代码拓扑；
* **多工具调用稀疏索引数学陷阱根治**：
  - 针对大模型返回的 `tool_calls` 切片可能带有稀疏或非零 index 的协议特征，摒弃危险的 `0..len-1` 连续下标假设，重构为 key 收集与 `sort.Ints` 升序遍历，确保所有高位索引工具调用 100% 完整捕获与并发调度；
* **SSE 思考流 10MB 动态缓冲与长文本容灾**：
  - 将 Go `bufio.Scanner` 默认 64KB 缓冲区扩容至 10MB 动态上限，全面防御 DeepSeek-R1、Claude 3.7 Sonnet 等超长心智思维链导致的 `bufio.ErrTooLong` 溢出截断，并显式拦截 `scanner.Err()` 抛出结构化网络错误；
* **受控文件工具 Sandbox 空指针 Panic 拦截**：
  - 在 `fstool.Execute` 顶部引入前置防御，未就绪或未初始化时优雅报错，严禁空指针解引用崩溃；
* **Swarm 算子化验证 60s 硬超时与 Windows 零黑框**：
  - `RunTDDValidation` 引入 60 秒绝对上下文超时控制与 `0x08000000` 零黑框标志位，根除测试子进程永久挂起与弹窗闪烁；
* **ShellExecuteW 异步脱机批处理与进程树自毁安全**：
  - 安装器与卸载器 `taskkill` 注入 `/T` 递归树杀，杜绝残留孤儿工具链进程；
  - 卸载器采用 Win32 API `ShellExecuteW` 异步脱机拉起延时清理批处理（`SW_HIDE`），彻底脱离父进程控制台管道生命周期，配合 `(goto) 2>nul & del "%~f0"` 实现干净彻底的无残留静默卸载自清理。

### 35. MCP 外部握手并发优化、守护协程防泄漏、Git 空格路径防御与时间戳规范化 (MCP Concurrency, Goroutine Leak Prevention & Path Spaces)
* **MCP Manager 启动长 I/O 锁粒度优化与无阻塞并发查询**：
  - 彻底将耗时可达 10 秒的子进程启动与 JSON-RPC 工具列表握手移至写锁外部，仅在注册与实例创建时持有临界区锁，彻底根除高并发状态探活与列表读取全局雪崩卡死缺陷；
* **StdioClient 协程悬挂根除与通道安全复用**：
  - `Stop()` 显式关闭 `stdout` 管道句柄，解决 Windows 平台底层管道未关闭致 `readLoop` 协程永久挂起的问题；
  - `Stop()` 唤醒所有 `pending` 阻塞信道，避免调用端死等；`Start()` 自动重置已关闭的 `stopChan`，赋予客户端安全重启复用能力；
* **终端流式执行 Context 监听协程对称退出防泄漏**：
  - 为 `ExecuteStream` 注入 `done` 通知信道与 `defer close(done)`，命令正常完成时立即退出守护协程，杜绝长会话中每次输入命令永久泄漏一个挂起协程的高危隐患；
* **Git Porcelain v2 空格文件名完整性还原**：
  - 针对带有空格的文件路径（如 `my test file.go`、`doc folder/a b.md`），基于列索引定界并通过 `strings.Join(parts[8:], " ")` 完整拼接，彻底根除因空格切割导致文件无法被 Git 识别与暂存的 Bug；
* **前端毫秒级时间戳跨端量级自适应防御**：
  - 统一在会话存储层进行数量级识别（`ts > 1e11` 判定为 JS 毫秒），杜绝未转换直接调用 `time.Unix` 导致时间溢出显示公元 58000+ 年；
* **会话安全删除幂等性保证**：
  - `Delete()` 显式捕获并忽略 `os.IsNotExist`，确保文件已被移除时操作幂等成功，消除前端偶发弹出红色非预期错误；
* **安装程序 PowerShell 快捷方式单引号注入防御**：
  - 对安装路径、桌面和开始菜单快捷方式路径中的单引号执行 `''` 安全转义，彻底防御特殊用户目录下安装失败的解析异常；
* **Skills 与 Rules 动态删除闭环与快捷键监听注销**：
  - 完善微内核配置存储层 `DeleteSkill` 与 `DeleteRule` API，前端设置面板补齐删除确认交互；全局快捷键采用具名函数并在组件卸载时安全移除。

### 36. 插件分段锁原子热替换、Windows 盘符归一化与并发任务取消保护 (Plugin Hot-Reload, Drive Normalization & Cancellation Safety)
* **工作区切换插件分段锁原子替换与执行引擎深度重载**：
  - 在微内核注册中心引入 `RegisterOrReplace` 契约，彻底根除切换工作区后旧算子工具拒绝覆盖而继续在旧目录读写代码的严重越权漏洞，并同步重载智能体自主执行引擎（`loop.ExecutionEngine`）；
* **Windows 驱动器盘符大小写归一化与文件树全路径沙箱防护**：
  - 彻底解决 `filepath.EvalSymlinks` 返回大写盘符（如 `D:`）与小写工作区路径（如 `d:`）在 `filepath.Rel` 计算时误判跨盘符逃逸导致文件树偶尔空白的缺陷；
* **上游模型网关 HTTP 状态码严格 Fail-Closed 拦截**：
  - 消除 `FetchUpstreamModels` 未校验状态码直接 Decode 导致上游 401/403/500 报错被静默吞掉并返回伪造空列表的问题，严格直接暴露底层真实错误堆栈；
* **单调递增任务序号防护异步取消句柄 (CancelFunc) 冲空**：
  - 在终端流式执行与智能体推理中注入 `taskID` 原子时序防护，杜绝前序任务退出时冲掉并发新任务的取消句柄导致界面中断按钮失效；
* **智能体自主推理 15 步上限自然收敛显式告警**：
  - 达到最大步数上限时主动向前端派发提示，拒绝静默伪完成；
* **全域弹窗全局 Esc 快捷键层级化阻断退出 (铁律 5 闭环)**：
  - 全局键盘监听器补齐 `Escape` 键处理，支持无缝退出设置面板、MCP 服务弹窗、Skill/Rule 弹窗与知识图谱弹窗；
* **算子参数名多别名自适应兼容与原子写临时文件防泄漏**：
  - `write_file` 与 `read_file` 同时兼容 `rel_path`、`path` 与 `file_path`，沙箱原子写入引入强生命周期 `isCleaned` 状态，彻底杜绝 `.tmp` 文件在重命名失败时残留。

### 37. MCP 悬挂通道空指针防御、代码审计 OOM 熔断与 HTTP 连接池复用治理 (MCP Nil Defense, Audit OOM Guard & HTTP Pooling)
* **MCP StdioClient 悬挂请求 nil 安全唤醒与强类型异常隔离**：
  - 彻底治理客户端停止时向挂起 channel 派发 `nil` 导致调用方访问 `resp.Error` 产生的 `SIGSEGV` 空指针解引用崩溃；在 `Stop` 中先唤醒 channel 防死锁，在 `sendRequest` 中拦截 `nil` 明确返回错误，且在 `Start`、`ListTools`、`CallTool` 中全量补齐安全判空；
* **安全审查器大文件 5MB 熔断与 WalkFunc 空指针守卫**：
  - 在 `RunSecurityAudit` 中将 `err != nil` 与 `info.IsDir()` 彻底解耦，杜绝权限异常时解引用 `nil info` 崩溃；限制单个待审源码体积不超过 5MB，阻断潜在超大文件读取引发的 OOM；
  - 在 TDD 验证中，若编译或执行失败，强制置 `failed >= 1`，彻底杜绝 0 失败假绿灯误导；
* **全局 HTTP Transport 与连接池长连接复用**：
  - 在 `internal/llm/client.go` 中提取单例 `sharedTransport` 与 `sharedLLMClient`，统一管理 TCP 连接池与 HTTP Keep-Alive，杜绝每次请求重复创建 Transport 导致的握手延迟与高频短连接端口耗尽；
* **全局用量遥测 (Telemetry) 调用链连通与大盘实效采集**：
  - 将 `telemetry.GetTracker().Record(...)` 无缝接入智能体多轮流式推理主循环，根据每轮推理耗时与生成 Token 实时核算指标，同时为 `GetUsageMetrics` 补齐 `sessionStore` 判空防御；
* **GitOps 快照 Stash ID 严格白名单与标准错误捕获**：
  - `RestoreSnapshot` 引入 `isValidStashID` 格式严格校验（`^stash@\{\d+\}$`），并在所有分支切换、检出与快照操作中统一采用 `CombinedOutput` 捕获完整 stderr 输出，告别抽象的 `exit status 1`；
* **执行引擎 ToolPlugin 返回值 nil 判空守卫**：
  - 在自主执行双环内核中增加对算子返回空结果（`res == nil`）的防御性检测，杜绝第三方插件未遵循强契约引发的崩溃；
* **会话 ID 白名单解耦与手动中断即时持久化**：
  - 移除会话存储中对 `sess_` 前缀的硬编码过滤，兼容所有合法扩展 ID；在前端点击手动中断时即时触发 `saveSession`，确保已生成的局部回复不丢失；并在 IPC 桥接层补齐对 `agent:interrupted` 事件的清理与回调触发。

### 38. 未追踪代码块安全丢弃、Windows 盘符全链路归一化与网络探活连接池治理 (Untracked Hunk Discard, Drive Letter Normalization & Pinger Pooling)
* **未追踪新文件 (Untracked Files) 行级 Hunk 丢弃安全回退机制**：
  - 在 `internal/diff/differ.go` 的 `DiscardHunkPatch` 与 `plugins/tool/git/git_tool.go` 的 `RestoreFile` 中，针对未加入 Git 索引的新增文件（补丁头 `--- /dev/null`），拦截 `git apply --reverse` 报错，并在工作区沙箱安全校验通过的前提下，安全物理移除未追踪文件，确保代码树与前端放弃改动意图无损对齐；
* **LSP 编译器诊断与 AST 拓扑扫描 Windows 盘符大小写全链路归一化**：
  - 彻底治理 `internal/lsp/diagnostics.go` 与 `internal/ast/scanner.go` 中因 Windows 盘符大小写差异（如 `D:` vs `d:`）导致 `filepath.Rel` 计算报错 `path escapes workspace sandbox` 的假阳性沙箱误杀，并在 `filepath.Walk` 回调首行增加 `info == nil` 空指针熔断；
* **网络探活工具连接池复用与本地回环协议智能自适应**：
  - 重构 `internal/network/pinger.go`，构建全局共享连接池 `http.Transport`，解决高频测速 socket 泄漏；对 `localhost`、`127.0.0.1`、`0.0.0.0` 等本地地址智能补齐 `http://` 避免 TLS 握手崩溃，探测响应浅读排空实现 HTTP Keep-Alive；
* **文件系统算子 (fs_control) 多别名全量兼容与空白路径防御**：
  - 在 `plugins/tool/fs/fs_tool.go` 中为大模型算子调用无缝支持 `path`、`rel_path` 与 `file_path` 三重别名解析与 `TrimSpace` 防御，规避模型参数漂移导致的执行中断；
* **前端全局 Esc 事件生命周期管理与渠道列表纯净同步**：
  - 在 `SettingsModal.vue` 中挂载全局键盘事件并在组件卸载时安全解绑；修正 `loadChannels` 赋值逻辑，确保在渠道被清空时前端能够真实展示纯净空状态；
* **深度思考抽屉空白渲染过滤与硬编码历史残留彻底根除**：
  - 在 `ChatCockpit.vue` 中为 `msg.thinking` 增加非空过滤（`msg.thinking.trim().length > 0`），杜绝空白思考框占位；彻底移除早期原型遗留的 `msg.id === 'msg_2'` 假条件，改动文件卡片完全基于真实的 `allModifiedFiles` 动态响应。

### 39. 进程树主动取消机制、工具调用 ID 协议守卫与 Git 变更纯净状态治理 (Process Tree Cancellation, Tool Call ID Guard & Git Working Tree Hygiene)
* **Windows 孤儿进程树级联终止 (`cmd.Cancel` 注入)**：
  - 在受控终端算子 `plugins/tool/terminal/terminal_tool.go` 的同步执行 `Execute` 与流式执行 `ExecuteStream` 中利用 Go 1.19+ 的 `cmd.Cancel` 字段，注入 `taskkill /F /T /PID` 强杀整棵子进程树，彻底根除 context 超时或中断后后台测试/开发服务器孤儿僵死占用端口的高危隐患；
* **大模型多轮推理工具调用 ID 与 Type 协议自愈保全**：
  - 在 `internal/llm/client.go` 流式聚合与 `app.go` 主推理循环中，针对开源模型流式分片缺失或稀疏发送的 `ToolCall` 注入防空生成与 `type: "function"` 默认值，彻底防御回传时触发上游 `400 Bad Request: Invalid tool_call_id`；
* **沙箱路径空字节截断攻击防御与空白字符修剪**：
  - 在 `internal/core/sandbox/fs.go` 的 `ValidatePath` 中注入 `\x00` 空字节拦截，并进行 `strings.TrimSpace` 路径修剪，防止绕过沙箱扩展名检查与路径穿透；
* **MCP Stdio 管道异常退出挂起通道安全唤醒**：
  - 在 `internal/mcp/stdio.go` 的 `readLoop` 退出时唤醒所有 `c.pending` 中的等待通道，防止调用端永久死锁挂起直至上下文超时；
* **Git 源码管理动态变更映射与纯净空状态治理 (铁律 0.5)**：
  - 彻底铲除 `LeftDrawer.vue` 中写死的 `main.go`、`app.go`、`wailsBridge.ts` 假数据，无缝对接 `gitStatus.working` 与 `gitStatus.untracked`，在工作区干净时呈现纯净优雅的空状态；
* **跨会话历史消息对称重置与切换时序响应**：
  - 在 `chatStore.ts` 的 `switchSession` 首行执行 `messages.value = []`，将 `currentSessionId` 默认值从假会话 `'sess1'` 重置为空字符串，并在左侧历史会话卡片与新建会话点击时立即触发 `switchSession`；
* **Diff 审查变更采纳物理暂存闭环 (`GitStage`)**：
  - 在 `wailsBridge.ts` 与 `DiffWorkspace.vue` 中打通 `wailsBridge.gitStage`，用户点击“采纳变更”时真正将修改文件加入 Git 暂存区（`git add`）。

### 40. Git 源码管理全交互闭环、TDD 进程树熔断、文件物理删除 Diff 容错与卸载器自删除锁治理 (Git Full Loop, TDD Tree Kill & Differ Resilience)
* **Git 抽屉源码管理真实提交与刷新全链路驱动**：
  - 彻底铲除 `frontend/src/components/LeftDrawer.vue` 中提交信息输入框与提交按钮无任何事件绑定的 Hollow UI 漏洞；补齐 `commitMessage` 双向绑定、回车快捷提交、`handleGitCommit` 调用链及提交成功后自动清空输入并全量刷新 Working Tree 状态；
* **Working Tree 状态结构体响应式映射与纯净空状态**：
  - 修复 `frontend/src/App.vue` 中将后端返回的 `GitFileStatus` 结构体切片直接当作字符串遍历渲染导致界面显示 `[object Object]` 并漏掉未追踪文件的缺陷；引入 `workingTreeFiles` 计算属性，归一化工作区变更与未追踪文件，并在工作区干净时呈现纯净空状态；
* **TDD 自动化测试跨平台孤儿进程树级联销毁 (`cmd.Cancel`)**：
  - 在 `internal/agent/swarm.go` 的 `RunTDDValidation` 中注入 `cmd.Cancel = func() error { ... taskkill /F /T /PID ... }`，确保当 60 秒硬超时或上下文取消触发时，Windows 下由 `go test` 编译生成的真实 `.test.exe` 进程及其整棵派生树被彻底递归销毁，杜绝孤儿测试进程僵死霸占 CPU 与端口；
* **文件物理删除状态下 Diff 引擎容错与补丁计算**：
  - 在 `internal/diff/differ.go` 的 `ComputeFileDiff` 中解除对本地文件必须物理存在的硬性限制；针对被删除的文件（`os.IsNotExist`），自动尝试比对 `git diff HEAD -- <relPath>`，确保代码审查与版本树能精准展示文件的负向变更差异；
* **文件系统算子动作指令大小写容错清洗**：
  - 在 `plugins/tool/fs/fs_tool.go` 中加入 `strings.ToLower(strings.TrimSpace(args.Action))` 容错清洗，防止大模型输出 `"Write"`、`"READ"` 时误报 `unknown action`；
* **智能体自主执行写文件影子快照保障**：
  - 在 `app.go` 主执行循环的 `write_file` 算子调度分支前，无缝注入 `snapshotMgr.CreateSnapshot`，为每次代码写入自动生成轻量 Git 影子快照，确保故障时可秒级无损回退；
* **网络探活服务端 5xx 异常状态码严格 Fail-Closed**：
  - 在 `internal/network/pinger.go` 中补全 HTTP 状态码校验，对上游网关返回的 500/502/503 等服务端崩溃明确判定为探活失败，杜绝误报低延迟；
* **工作区热切换多任务原子取消**：
  - 在 `app.go` 的 `SetWorkspace` 入口处同时触发 `agentCancel()` 与 `terminalCancel()`，防止旧工作区未完成的长推理与终端阻塞任务跨工作区交织；
* **快照编号自适应格式化与防注入守卫**：
  - 在 `internal/gitops/gitops.go` 的 `RestoreSnapshot` 中为纯数字 Stash 编号（如 `"0"`）自适应组装为 `stash@{0}`，兼顾灵活性与严格校验；
* **Windows 单文件卸载器脱机批处理自删除锁机制治理**：
  - 彻底治理卸载器直接运行批处理由于自身文件被操作系统锁死导致自删除失败的物理缺陷；通过独立临时文件重命名解绑与延时循环，保障卸载后零字节残留。

### 41. Git 分支检出双横杠陷阱、MCP 管道防死锁排水与流式中断隔离治理 (Git Branch Fix, MCP Stderr Drain & Cancel Isolation)
* **Git 分支检出破坏性参数错误根治 (`git checkout <branch>`)**：
  - 彻底移除 `internal/gitops/gitops.go` 中 `git checkout -- <name>` 错将分支当作路径解析导致的致命故障，结合前置参数过滤校验确保分支自由无损切换；
* **MCP 外部进程 Stderr 异步非阻塞排水防死锁**：
  - 在 `internal/mcp/stdio.go` 中为子进程挂载独立后台协程消费 `cmd.StderrPipe()`，彻底解决外部 Node/Python 插件日志写满管道缓冲区（4KB~64KB）引发的全局进程死锁；
* **文件树点击相对路径精确传递 (`node.path`)**：
  - 修复 `frontend/src/components/LeftDrawer.vue` 中点击文件错误传递 `node.name` 导致跨层级文件 Diff 全量报错的缺陷，确保代码工作区能够秒级加载任意子目录文件的物理 Diff；
* **智能体流式推理中断事件注入会话标识与监听器彻底注销**：
  - 在 `app.go` 的 `CancelAgentStream` 中显式回传 `session_id`，在前端 `wailsBridge.ts` 中建立鲁棒容错注销机制，彻底根治中断后监听器永久泄漏与流式状态挂死；
* **推理中断空消息拦截与终结事件冲突治理**：
  - 智能体循环在早期被中断且未产出任何实质内容时，自动跳过保存无意义的空 assistant 消息，且被中断时不派发 `agent:done`，防止状态撕裂；
* **全新空 Git 仓库 (无 HEAD 提交) Diff 引擎自适应容错**：
  - 在 `internal/diff/differ.go` 中引入 `hasGitHead` 探测，针对初次初始化的空仓库自动回退为新文件比对，杜绝抛出 `fatal: bad revision 'HEAD'` 崩溃；
* **沙箱空路径越权与根目录覆盖拦截**：
  - 在 `internal/core/sandbox/fs.go` 的 `ValidatePath` 中禁止空路径，并在 `AtomicWriteFile` 中拦截向工作区根目录自身的破坏性写入；
* **Diff 视窗采纳与放弃改动全局 Working Tree 即时响应**：
  - 在 `chatStore` 中引入 `gitVersion` 响应式信号并在 `DiffWorkspace.vue` 动作后广播，驱动 `LeftDrawer` 与主工作区变更文件列表即时刷新；
* **单文件安装向导自动化测试沙箱纯净隔离**：
  - 在 `cmd/installer/main.go` 中识别 `--silent-install-dir` 测试标志，跳过创建用户桌面快捷方式与写入全局注册表，彻底杜绝测试污染生产环境；
* **编译器语法诊断子进程超时级联树杀 (`cmd.Cancel`)**：
  - 在 `internal/lsp/diagnostics.go` 中为 Go、TypeScript 与 Python 诊断执行注入 Windows 递归树杀，杜绝 4s 超时后孤儿编译器进程僵死。

### 42. 会话更新时序保序、Windows 设备保留名防御与零假凭据架构防线 (Session Sorting, Windows Device Name Defense & Zero Demo Hardening)
* **会话列表更新时序强制降序排列 (`Store.List`)**：
  - 彻底治理文件系统遍历无序导致的会话列表乱序跳动，在 `internal/session/store.go` 中引入 `sort.Slice` 按 `UpdatedAt` 降序排列，并在 `Store.Save` 中提供时间戳非零保留与毫秒格式化，保障前端会话历史体验严格有序；
* **Windows 操作系统保留设备名称应用层坚固防线**：
  - 在 `sanitizeID` 中增加对 Windows 底层保留设备名（`CON, PRN, AUX, NUL, COM1..9, LPT1..9` 忽略大小写及后缀）的黑名单拦截与 `<>:"/\|?*` 非法字符过滤，杜绝应用层请求触发底层文件系统不可挽回的拒绝与句柄悬挂；
* **原子写入持久化父目录自动级联创建保障**：
  - 在 `internal/session/store.go` 的 `atomicWriteSession` 与 `internal/config/extra_stores.go` 的 `atomicWriteConfig` 写入临时文件前强制执行 `os.MkdirAll(filepath.Dir(filePath), 0755)`，防止跨模块调用或环境清理后父目录缺失引发的运行时崩溃；
* **双环执行引擎模型工具空输出网关契约守卫**：
  - 在 `internal/core/loop/engine.go` 将工具输出回填进上下文时，对纯空字符串注入安全保全说明，彻底防御上游 OpenAI/Anthropic/DeepSeek 网关因 `messages[x].content` 为空而抛出 `400 Bad Request`；
* **物理撤回文件路径沙箱边界与根目录误伤防护**：
  - 在 `plugins/tool/git/git_tool.go` 中对 `RestoreFile`、`StageFile` 与 `UnstageFile` 增加严格非空校验与工作区沙箱绝对路径规范化，拦截 `.` 与超出工作区的非法路径；
* **工作区 AST 语法树扫描根路径 Fail-Closed 预检**：
  - 在 `internal/ast/scanner.go` 中执行 `filepath.Walk` 之前增加 `os.Stat(rootDir)` 真实检查，对不存在的目录直接返回清晰错误，严禁静默降级为假成功与空结果；
* **遥测大盘模型名称归一化与负数边界防御**：
  - 在 `internal/telemetry/tracker.go` 中对空模型名自动归一化为 `"unknown"`，对 Tokens 消耗、执行耗时与活跃会话数实施非负数防护；
* **主渠道切换异步等待与最新状态自动同步**：
  - 改造 `frontend/src/components/SettingsModal.vue` 中的 `setPrimaryChannel` 为 `async`，`await wailsBridge.saveChannel` 并立即触发 `await loadChannels()`，保持前后端状态原子同步；
* **流式 SSE 上游错误报文快速拦截解析与派发**：
  - 在 `plugins/provider/openai/openai_provider.go` 中引入针对上游 `error` 报文的探测解析，遇到限流或网关异常第一时间向前端派发 `StreamChunk{Error: ...}` 并平滑退出，杜绝流式静默截断；
* **空 Diff 计算文件拦截与零冗余 IPC 调用**：
  - 在 `frontend/src/App.vue` 的 `loadDiff` 顶部增加空文件拦截，防止空路径触发后端无效 Diff 计算与前端控制台报错；
* **全量 Demo 假数据、泄露密钥与预填凭据彻底清洗 (铁律 0.5)**：
  - 全面清退 `frontend/index.html`、`prototype/index.html` 与 `web_prototype.html` 中所有硬编码的假 Token、泄露 API Key 与假会话，所有敏感凭据重置为纯净空字符串，展示层统一执行 `已安全加密存储 (AES-GCM)` 脱敏。

---

## 五、Git 管道鲁棒性、无 HEAD 容错与微内核防崩 (迭代 43 - 47)

### 43. 工具空输出防御、RevertFile 撤销防误删、真实快照时间戳与事件全量清理 (Empty Tool Content Defense, RevertFile Safe Deletion, Real Snapshot Timestamps & Event Cleanup)
* **主智能体执行循环工具空输出保全防上游崩溃 (`app.go`)**：
  - 当大模型调用的工具无控制台输出时，自动注入语义说明 `tool [%s] executed successfully with empty output`，并在 `pkg/protocol/openai_adapter.go` 建立双层保全，彻底根除 OpenAI / Anthropic 等网关返回 `400 Bad Request: 'messages[x].content' cannot be empty`；
* **`RevertFile` 空仓库无 HEAD 撤销防误删与状态精准识别**：
  - 彻底治理旧逻辑在未产生初次提交的空仓库中执行 `git checkout HEAD` 报错后盲目调用 `os.Remove(validPath)` 导致代码被物理毁灭删除的严重缺陷；引入 `git status --porcelain` 精确识别 `??` 未追踪文件，仅对真正未追踪新文件安全清理，已追踪文件优先使用 `git restore`，杜绝误伤；
* **沙箱路径逃逸检测放行以双点开头的合法文件名**：
  - 修复 `internal/core/sandbox/fs.go` 与 `internal/diff/differ.go` 中直接使用 `strings.HasPrefix(rel, "..")` 导致工作区内合法文件（如 `..config.json`）被误杀的缺陷，精准判定跃迁逃逸；
* **沙箱与文件工具 `ListDir` 完美支持根目录枚举**：
  - 针对大模型传入空路径或 `.` 列出工作区根目录的操作，`ListDir` 自动安全映射为沙箱根目录，解决 `empty path is not allowed` 阻断大模型目录感知的问题；
* **Git 快照真实提交时间戳提取 (铁律 0.5)**：
  - 铲除 `internal/gitops/gitops.go` 中使用 `time.Now()` 伪造快照时间的弊端，通过 `git log -g --pretty=format:"%gd|%ct|%gs" refs/stash` 获取真实的 Unix 秒级时间戳与时分秒格式化；
* **未追踪空文件 Diff 精确计算与 Windows CRLF 换行清洗**：
  - 针对 0 字节空文件计算出 0 行变动与空 Hunk，防止生成无效 patch 损坏应用；并自动去除 Windows 换行符 `\r`，保障 patch 干净规范；
* **MCP `StdioClient.readLoop` 空指针防御**：
  - 在 `internal/mcp/stdio.go` 的 `readLoop` 入口增加 `c.stdout == nil` 保护，防止进程异常启动或提前关闭引发空指针解引用；
* **终端工具全空格指令严格拦截**：
  - 在 `plugins/tool/terminal/terminal_tool.go` 的 `Execute` 与 `ExecuteStream` 中使用 `strings.TrimSpace` 过滤全空格指令，防止空白指令下发 CMD 挂起；
* **SSE 流式传输网关错误探测与即时报错**：
  - 在 `internal/llm/client.go` 中解析 SSE 数据时优先探测 `error` 报文（限流、欠费或敏感词拦截），直接触发 `OnError` 中断，杜绝虚假成功；
* **前端全局事件监听器统一声明式注销与零残留**：
  - 在 `frontend/src/core/wailsBridge.ts` 中以声明式数组统一注册与注销 `agent:start`、`agent:files_changed`、`lsp:diagnostic` 等全部事件，彻底根除长期运行中的监听器泄漏。

### 44. 目录防覆盖防御、Git 暂存区感知提交、MCP 锁粒度优化与全链路无黑框防护 (Directory Overwrite Defense, Staged Git Commit, MCP Lock Granularity Optimization & Full-Link No-Window Protection)
* **沙箱原子文件写入（`AtomicWriteFile`）覆盖目录前置拦截与防误删空目录 (`internal/core/sandbox/fs.go`)**：
  - 彻底治理当目标路径已存在且为目录时，Windows 下 `os.Rename` 失败后回退触发 `os.Remove(validated)` 导致现有空目录被物理删除的致命缺陷；在写临时文件前显式增加 `os.Stat(validated).IsDir()` 前置防御拦截；
* **`GitCommit` 暂存区状态感知提交与用户选择保护 (`app.go`)**：
  - 消除无脑 `git add -A` 强行覆盖用户暂存区选择的缺陷；前置调用 `git diff --cached --quiet` 探测，若用户已在 Git 抽屉中手动暂存部分改动，则仅提交已暂存内容；仅在暂存区完全为空时才做全量暂存兜底；
* **Wails 原生 IPC 桥接层补齐 `GitUnstage` 取消暂存接口 (`app.go` & `wailsBridge.ts`)**：
  - 在微内核与前端桥接层暴露 `GitUnstage(filePath)` 接口，优先调用 `git restore --staged -- filePath` 并优雅降级至 `git reset HEAD`，实现 Git 暂存状态的双向自由流转；
* **Git 快照管理器与 HTTP 差异服务注入 Windows 无黑框配置 (`internal/core/sandbox/snapshot.go` & `internal/transport/http/server.go`)**：
  - 为 `SnapshotManager.execGit` 与 `handleFsOriginal` 注入 `SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}`，消除用户切换分支或预览快照时的黑框弹窗闪烁；同时为 `handleFsOriginal` 补充沙箱路径越界校验；
* **MCP 协议管理器停止服务互斥锁粒度优化 (`internal/mcp/manager.go`)**：
  - 将 `StopServer` 与 `StopAll` 中包含最长 1 秒超时等待的 `client.Stop()` 移出 `m.mu.Lock()` 互斥临界区，避免高并发下大模型工具调用与探活请求被长时间阻塞死锁；
* **AST 拓扑扫描器根路径规范化基准对齐 (`internal/ast/scanner.go`)**：
  - 统一 `ScanWorkspaceAST` 入口与递归遍历闭包中的 `rootDir` 清洗基准（`filepath.Clean`），解决 Windows 下正反斜杠混用引发 `filepath.Rel` 频繁报错并退化为纯文件名的问题；
* **会话元数据零时间戳东八区怪异 `"08:00"` 清洗 (`internal/session/store.go`)**：
  - 针对新建尚未更新的会话（`UpdatedAt <= 0`），格式化时返回空字符串，彻底消除东八区时间换算后误显示为 `"08:00"` 的怪异表现；
* **前端 IPC 桥接层 `gitCommit` 严格遵循 Fail-Closed (铁律 0.5)**：
  - 清退微内核未就绪时伪造的 `'Committed successfully'` 假成功，改为抛出真实错误并由 UI 弹窗捕获提示；
* **蜂群多智能体 TDD 验证非 Windows 环境进程清理保全 (`internal/agent/swarm.go`)**：
  - 为非 Windows 操作系统补充 `cmd.Cancel = func() error { return cmd.Process.Kill() }`，确保超时或任务取消时测试子进程被彻底杀灭，杜绝孤儿僵死进程。

### 45. Git Porcelain v2 重命名解析修复、无 HEAD 初始仓库安全撤回、MCP 并发 I/O 锁优化与暂存区前端闭环 (Git Porcelain v2 Rename Fix, No-HEAD Revert Safety, MCP Concurrent I/O Lock Optimization & Staged Drawer Integration)
* **Git Porcelain v2 重命名字段偏移与路径颠倒彻底根除 (`plugins/tool/git/git_tool.go`)**：
  - 修复 `parsePorcelainV2` 解析重命名行时将索引偏移第 8 项（`R100` 分数）误当成旧路径并将新旧路径颠倒的严重问题；严格按照制表符 `\t` 分割提取状态元数据、新路径与旧路径，保障包含空格及复杂文件名的重命名完整可识别；
* **`RestoreFile` 撤销防误删机制 (`plugins/tool/git/git_tool.go`)**：
  - 根除 `git restore` 失败时盲目调用 `os.Remove(cleanAbs)` 的数据毁灭风险；前置调用 `git status --porcelain`，仅在文件证据确凿属于未追踪文件（`??`）时才允许物理删除，防止误删用户已追踪的代码改动；
* **初建无 HEAD 仓库（`git init`）已暂存文件安全回退 (`app.go` & `internal/diff/differ.go`)**：
  - 导出 `HasGitHead` 探测方法；在尚未产生首个提交的新仓库中，针对已暂存（`A`）文件自动降级调用 `git rm --cached -f -- <path>` 并物理删除，解决 `fatal: could not resolve HEAD` 导致新工程无法撤销暂存新增文件的卡死问题；
* **HTTP 微内核服务空指针崩溃与沙箱逃逸全面封堵 (`internal/transport/http/server.go`)**：
  - 在 `handleFsRead`、`handleFsOriginal`、`handleFsWrite` 中补齐 `if s.sandbox == nil` 前置空指针防御，阻断早期访问时的 SIGSEGV；在 `handleSnapshotRollback` 补充 `s.snapshotMgr == nil` 判空与 `s.sandbox.ValidatePath(req.Path)` 沙箱逃逸防御；
* **MCP 协议管理器 `GetAllTools` 读锁外置并发优化 (`internal/mcp/manager.go`)**：
  - 将所有外部 MCP 进程的阻塞 `ListTools(ctx)` stdio I/O 移出 `m.mu.RLock()` 临界区，读锁内仅完成客户端映射浅拷贝，释放读锁后再并发查询工具列表，彻底消除写锁被长时间挂起的并发雪崩；
* **前端 IPC 桥接层严格遵循 Fail-Closed (铁律 0.5) (`frontend/src/core/wailsBridge.ts`)**：
  - 清退 `executeTerminalStream` 在后端微内核断开时伪造的 `[local] executed` 假数据，`gitStage` 与 `gitUnstage` 补齐微内核未就绪检查与异常抛出；
* **工程文件树展开子节点 Diff 路径穿透修复 (`frontend/src/components/LeftDrawer.vue`)**：
  - 修正点击子目录嵌套文件时调用 `store.openDiff(sub.name)` 仅传递纯文件名的问题，改为传递完整相对路径 `sub.path`，使多层级目录文件可正常打开 Diff 审查；
* **Git 源码管理侧边栏暂存区（STAGED）展示与取消暂存闭环 (`frontend/src/components/LeftDrawer.vue` & `frontend/src/App.vue`)**：
  - 在抽屉中完整补齐 `STAGED (已暂存)` 文件列表、变更类型标签与 `[-]` 取消暂存快捷按钮，并在 `<script setup>` 中注入 `stagedTreeFiles`、`unstageFileAction` 与 `revertFileAction` 空值保护，实现 Git 暂存区双向流转闭环；
* **LSP 编译器诊断合法双点文件名放行与跨平台进程树清理 (`internal/lsp/diagnostics.go`)**：
  - 修复 `strings.HasPrefix(rel, "..")` 误伤形如 `..sample.go` 合法文件的问题，规范化为 `rel == ".." || strings.HasPrefix(rel, ".." + Separator) || strings.HasPrefix(rel, "../")`；为非 Windows 系统补齐 `cmd.Cancel` 进程杀灭，防止语法诊断超时遗留孤儿进程。

### 46. 无 HEAD 仓库 AM 状态 Diff 修正、安全审计大小写双向归一化、取消暂存平滑降级与跨平台进程自愈 (No-HEAD AM Diff Fix, Audit Case Normalization, Unstage Fast-Path & Process Self-Healing)
* **无 HEAD 仓库暂存后修改（AM 状态）Diff 准确计算 (`internal/diff/differ.go`)**：
  - 修复 `ComputeFileDiff` 在刚 `git init` 尚无 HEAD 提交的新仓库中，针对暂存后再次修改的文件（`AM` 状态），由于严格匹配 `strings.HasPrefix(statusStr, "A ")` 导致匹配失败、误判为“未修改干净文件”的分支漏洞；改为匹配 `strings.HasPrefix(statusStr, "A")`，确保新增并修改的文件完整生成有效行级比对与差异块；
* **无 HEAD 仓库 `GitUnstage` 取消暂存安全降级 (`app.go`)**：
  - 修复前端点击取消暂存按钮时，在无 HEAD 仓库直接执行 `git restore --staged` 或 `git reset HEAD` 报错 `fatal: could not resolve HEAD`（退出码 128）的问题；增加 `!diff.HasGitHead(a.workspace)` 探测，在无 HEAD 状态下平滑降级执行 `git rm --cached -f -- <path>`，使新创仓库暂存撤销顺畅可用；
* **代码安全审计大写关键词双向小写规范化匹配 (`internal/agent/swarm.go`)**：
  - 根除 `RunSecurityAudit` 中规则模式串（如 `exec.Command("cmd", "/c", "del"`）与输入字符串比较时仅对输入做 `strings.ToLower`、而对模式串未转小写导致大写规则永久无法命中的死逻辑漏洞；改为双向小写规范化比较 `strings.Contains(strings.ToLower(str), strings.ToLower(kw))`；
* **工作区自身名称为敏感词时的遍历防御 (`internal/agent/swarm.go`)**：
  - 修复安全审计在 `filepath.Walk` 检查跳过 `build`/`bin`/`dist` 目录时未排除工作区根目录自身的逻辑缺陷；当用户工作区根目录名恰好为 `build` 时，防止第 0 步自杀性中断导致 0 文件假扫描，补充 `cleanPath != cleanWs` 根节点保护；
* **工程文件树合法双点目录名称放行 (`app.go`)**：
  - 修复 `buildFileTreeInternal` 使用 `strings.HasPrefix(relWs, "..")` 误判类似 `..cache` 或 `..temp` 合法前缀目录为跨目录穿越的问题；规范化为 `relWs == ".." || strings.HasPrefix(relWs, ".."+Separator) || strings.HasPrefix(relWs, "../")`；
* **无 HEAD 初始仓库安全快照防崩溃 (`internal/gitops/gitops.go`)**：
  - 修复 `CreateSnapshot` 在刚初始化的仓库中直接执行 `git stash push` 导致报错 `fatal: You do not have the initial commit yet` 的问题；前置 `diff.HasGitHead` 探测，若尚无首个提交则安全返回友好错误，防止后续误判；
* **前端 IPC 桥接层 Fail-Closed 真实反馈全面覆盖 (铁律 0.5) (`frontend/src/core/wailsBridge.ts`)**：
  - 根除 `revertFile`、`applyDiffHunk`、`discardDiffHunk` 在桌面微内核未连接时静默吞掉异常的假成功隐患；当微内核未就绪时严格抛出 `microkernel not connected`，彻底阻断静默降级；
* **文件系统工具 schema 与空路径容错 (`plugins/tool/fs/fs_tool.go`)**：
  - 修复 `fs_control` schema 将 `path` 强制标注为必填导致大模型调用 `action="list"` 查看根目录时被校验拦截的问题；移出必填列表并补充注释，后端对于空路径自动缺省为当前目录 `.`；
* **终端执行跨平台进程超时清理 (`plugins/tool/terminal/terminal_tool.go`)**：
  - 为 Linux/macOS 非 Windows 环境下的 `Execute` 和 `ExecuteStream` 补充 `cmd.Cancel` 杀死进程函数，防止命令超时或上下文取消后产生僵死孤儿进程；
* **安装程序卸载器更新原子性与错误提示 (`cmd/installer/main.go`)**：
  - 修复安装器在覆写目标目录 `uninstall.exe` 时未先执行 `os.Remove` 且未捕获写入错误的问题；先清理残留，并在写入失败时弹出原生提示框中断安装，保障安装过程的原子完整性。

### 47. HTTP 引擎空指针熔断、Git 分支全字符注入防御、已删除文件 Diff 状态机与 MCP 插件空守卫 (HTTP Nil Engine Guard, Git Branch Sanitization, Deleted Diff State Machine & MCP Nil Guards)
* **HTTP 网关未初始化前置熔断防 goroutine panic 崩溃 (`internal/transport/http/server.go`)**：
  - 修复 `handleChatStream` 在未校验 `s.engine == nil` 的情况下派发异步子协程调用 `s.engine.Execute` 导致触发 SIGSEGV（信号 `0xc0000005`）崩溃退出的严重隐患；补齐 `s.engine == nil` 前置熔断；同时为 `handleGitStage`、`handleGitUnstage`、`handleGitRestore` 补齐 `s.gitTool == nil` 守卫，返回标准 500 错误而非进程崩溃；
* **ReAct 自主推理引擎全域空指针防护 (`internal/core/loop/engine.go`)**：
  - 在 `ExecutionEngine.Execute` 入口处全面补齐 `e == nil`、`e.registry == nil` 与 `req == nil` 防御，主动派发 `EventError` 并返回强类型错误，阻断底层解引用崩溃；
* **Git 分支全量注入防御 (`internal/gitops/gitops.go`)**：
  - 严格遵循 Git Revision 规约升级 `isValidBranchName`：严禁注入修订版本范围运算符 `..`（如 `main..feature`）、前导/后置斜杠 `/`、锁文件后缀 `.lock`、连续双斜杠 `//`、单独符号 `@` 以及空字节 `\x00`，杜绝命令行参数注入与文件系统非法 Ref 写入；
* **已删除文件 Diff 状态机差异展示 (`internal/diff/differ.go`)**：
  - 修复物理文件在磁盘删除（`os.IsNotExist`）但 Git 仍保留跟踪记录时，旧代码直接抛出 `file does not exist` 拒绝计算的逻辑缺陷；前置读取 `git status --porcelain` 识别已删除状态，准确输出包含 `文件已被物理删除` 与 `@@ 文件已从磁盘中移除 (Deleted) @@` 的行级 Diff，保障审查界面平滑可用；
* **丢弃新增补丁时的 Git 暂存区索引同步 (`internal/diff/differ.go`)**：
  - 修复 `DiscardHunkPatch` 在丢弃新增文件补丁（`--- /dev/null`）时仅物理删除了磁盘文件、遗留暂存区幽灵索引（`D `）的漏洞；在物理删除前自动执行 `git rm --cached -f -- <path>` 同步清理索引；
* **Git 受控插件无 HEAD 仓库撤销及降级检出 (`plugins/tool/git/git_tool.go`)**：
  - 为 `Tool.RestoreFile` 补齐 `!diff.HasGitHead` 探测与 `git rm --cached -f` 快速通道，解决新创仓库撤销暂存文件报错的问题，并在 `git restore` 失败时自动降级调用 `git checkout --`；
* **MCP 全局管理器空指针守卫 (`internal/mcp/manager.go`)**：
  - 为 `TestServer`、`StopServer`、`StopAll`、`GetAllTools` 与 `CallTool` 全量增加 `m == nil` 空值守卫，杜绝在管理器尚未初始化时读写读写锁或解引用字段引发的 panic；
* **桌面端智能体工具调用链非空守卫 (`app.go`)**：
  - 在 `SendMessage` 智能体执行流中，对 `a.termTool`、`a.sandbox`、`a.gitTool` 执行严格前置判空保护，避免因工作区加载延迟或工具未就绪导致后台协程异常；
* **前端 IPC 桥接层配置变更方法严格 Fail-Closed (铁律 0.5) (`frontend/src/core/wailsBridge.ts`)**：
  - 为 `setWorkspace`、`saveSession`、`deleteSession`、`saveChannel`、`deleteChannel`、`saveMCP`、`deleteMCP`、`saveSkill`、`deleteSkill`、`saveRule`、`deleteRule` 全量补齐微内核连接性校验，在微内核断开时强制抛出 `microkernel not connected` 错误，杜绝静默失败与假成功；
* **单文件安装器测试模式安全隔离 (`cmd/installer/main.go`)**：
  - 为安装器的旧进程清理命令注入 `!isTestingMode` 守卫，避免单元测试或临时目录探活时误杀用户正在正常运行的 湉码 实例。

---

## 六、工作区动态绑定、双栈 TDD 与自主交付收敛 (迭代 48 - 52)

### 48. 工作区动态重绑定、Tab 内存持久化、TDD 双栈闭环与文件树按需懒加载 (Workspace Rebind, Tab Persistence, Dual-Stack TDD & Lazy File Tree)
* **工作区热切换重绑定遗漏检索算子修复 (`app.go`)**：
  - 修复在 `SetWorkspace(absDir)` 中仅热替换了 `git/fs/terminal` 算子，而遗漏重新注册 `searchtool.NewTool(sb)` 的严重漏洞，彻底杜绝切换工程目录后智能体 `search_workspace` 仍持续在旧项目根目录下跨目录搜寻代码的越权缺陷；同时在 `NewApp` 与 `SetWorkspace` 中统一升级 `engine.Verify` 闭包，使其动态引用 `a.workspace` 确保 TDD 验证靶向新工作区；
* **Monaco 多文件标签页内存缓冲区无损持久化 (`frontend/src/stores/workbench.ts`)**：
  - 在 `EditorTabItem` 实体中扩展 `content?: string` 字段，彻底修复此前在多文件标签页（Tabs）间切换时调用 `loadEditor()` 强制重读物理磁盘覆盖 `editorContent` 导致未保存代码草稿被静默销毁的致命体验缺陷；
  - 切换 Tab 时先将活动编辑器的内容与脏标记落入当前 Tab 缓存；激活目标 Tab 时优先还原内存缓冲；
* **关闭未保存标签页暖色弹窗阻断拦截 (铁律 5 闭环) (`DiffWorkspace.vue`)**：
  - 当用户点击关闭处于 `dirty: true` 的未保存标签页时，强行拦截关闭动作，严禁使用原生 `confirm()`，而是呼出屏幕居中、支持 Esc 退出、具备显式 `[X]` 的 Warm Cream 暖色模态窗；提供「取消」、「放弃修改并关闭」与「保存并关闭」三路明确选择；
* **TDD 双栈级联验证彻底打破互斥偏见 (`internal/agent/swarm.go`)**：
  - 彻底重构 `RunTDDValidation`，打破早期 `if hasGoMod ... else ...` 仅能二选一的互斥局限；针对类似 湉码（Go 原生微内核 + `frontend/package.json` 前端工作区）的混合全栈仓库，同时级联执行 `go test -v ./...` 与 `npm test`（智能探测根目录或 `frontend/` 目录）；
  - 聚合输出两个套件的完整日志与分项统计（`totalPassed` / `totalFailed`），任一套件失败全盘裁定为 `FAIL`，杜绝前端单测挂掉但因 Go 单测通过而亮绿灯的假成功；
* **工程文件树按需异步懒加载与大项目防截断 (`app_shell.go` & `FileTreeNode.vue`)**：
  - 根除初次启动时无脑全量遍历 12 层导致大项目 DOM 爆炸及触发 2000 节点全局熔断截断目录树的硬伤；将每次扫描深度严格受控为 1 层直接子级；
  - 前端点击目录展开时，按需异步下钻调用 `wailsBridge.getFileTree(node.path)` 并实时挂载，搭配优雅的「加载中...」与「(空目录)」指示，实现亿级规模超大工程的秒级轻量加载；
* **会话默认策略收敛至只读审查 (`analyze`) (`frontend/src/stores/workbench.ts`)**：
  - 将新建会话与默认执行策略设为 `analyze`（只读审查，先看地图，拦截写盘与非清单深层探索），严格贯彻铁律；若需修改代码，引导用户在驾驶舱或发送栏显式自主选定 `implement` 或 `tdd`。

### 49. 开发者工作区全真检索、策略跳过守卫、实验特性诚实标注与 V1 边界矩阵对齐 (Human Search, Strategy Guard, Lab Badging & Matrix Realignment)
* **开发者工作区一等全局检索视窗 (`app_shell.go`, `DiffWorkspace.vue`, `workbench.ts`)**：
  - 彻底终结“大模型有 `search_workspace` 算子而人类开发者抓瞎”的体验鸿沟；在左侧侧边栏引入 **📁 目录 (Tree)** 与 **🔍 检索 (Search)** 双页签自由切换；
  - 严格通过微内核 `host.Registry.GetTool("tool.search")` 统一步入，提供 **`grep` (代码/文本内容精确匹配)** 与 **`find` (文件名通配查找)**；
  - 检索结果以高亮卡片呈现真实相对路径、匹配行号与代码片段；点击结果一键在 Monaco 编辑器中打开目标文件并高亮聚焦该行；在文件树筛选未命中时，提供一键跳转全局检索的快捷指引；
* **策略跳过死循环修复与实现前置检索约束 (`frontend/src/App.vue` & `internal/core/loop/strategy.go`)**：
  - 彻底修复策略选择器中点击「跳过」反而激活直接改代码（implement）导致用户陷入循环的逻辑漏洞；点击「跳过」严格坚守默认安全底线——**保持默认只读审查 (`analyze`)**；
  - 在 `implement` 策略的系统提示词中注入刚性约束：“修改代码前必须先检索定位：优先使用 search_workspace (grep/find) 或 read_file 查明现有上下文与代码定义，严禁在未检索或未阅读目标文件的情况下盲改盲写！”；
* **非核心/单语言特性诚实打标 `[实验特性]` (`ActivityBar.vue`)**：
  - 对非主路径与仅限单语言的辅助功能（Token 用量看板、Go AST 知识图谱）在活动栏与悬停气泡中显式前置 **`[实验特性]`**，并追加 **`β`** 徽标，杜绝在主界面向开发者过度承诺未成熟特性；
* **真实双栈 TDD 测试脚本闭环与执行超时防护 (`frontend/package.json` & `internal/agent/swarm.go`)**：
  - 在 `frontend/package.json` 中配置原生断言测试脚本 `"test": "node scripts/assert-relative-assets.mjs"`，使得双栈级联 TDD 在 湉码 自身仓库中真正能跑通前端资产测试，拒绝“空脚本假装有前端测试”；
  - 将 TDD 级联测试超时上限由 60 秒扩展至 120 秒，彻底防御 Windows 平台下并发冷编译与测试耗时导致的意外超时假失败；
* **V1 特性边界矩阵诚实全面重构 (`docs/V1_FEATURE_BOUNDARY_MATRIX.md`)**：
  - 彻底打破“只写了代码就标 🟢 达标交付”的自欺欺人假象；依照“主路径可稳定独立完成、失败有交代、不靠用户猜”的硬性验收标准，将处于半成品态的链路（对话工作流、策略状态机、工作区检索、文件树、Monaco、Diff 暂存、Git 流程、TDD 验证、MCP/设置）如实标记为 **🟡 半成品**；仅保留单文件安装器/密钥持久化与沙箱安全为 **🟢 可用**，用量监控与代码图谱下沉为 **🔴 实验特性**。

### 50. 剥离形式主义外壳、主界面五大极简入口与设置中枢真实化治理 (De-Bloating Fake Shells, 5 Core Views & Honest Settings Hub)
* **主界面绝对聚焦：活动栏仅留 5 大核心入口 (`ActivityBar.vue`)**：
  - 根绝“把摆设功能塞满侧边栏冒充全能 IDE”的虚浮作风；从活动栏彻底移除用量大盘、AST 拓扑图谱与冗余的 MCP 快捷按钮；
  - 活动栏严格收敛为主工作流 5 大真实入口：**`💬 对话`**、**`📁 文件`**、**`🌿 Git`**、**`$_ 终端`** 与 **`⚙️ 设置`**，视觉清爽纯粹，零认知杂音；
* **终端抽屉纯粹化：移除前端假 Trace 流水账 (`TerminalDrawer.vue`)**：
  - 彻底移除终端抽屉中的第二页“Agent 执行链路”（此前仅为前端组件自身的 client-side push 伪流水，非内核真实分布式追踪）；
  - 终端抽屉保持 100% 纯粹真实：仅保留与 Windows 原生沙箱直连的 powershell/cmd 控制台，支持实时无黑框命令执行、状态感知、一键终止与清屏；
* **顶栏形式主义徽章清理 (`ChatCockpit.vue`)**：
  - 从对话顶栏移除无法保证模型严格遵从的只读“📜 宪法: X项”徽章，杜绝虚假的仪式感；规则与技能的真实挂载与开关完全归集至设置中枢；
* **全局快捷栏与斜杠指令去伪存真 (`workbench.ts`)**：
  - 从 `Ctrl+K` 快速启动栏中移除实验性的“打开知识图谱”；从 `/` 指令候选中移除基于字符串弱匹配的伪安全审查 `/audit`，只保留真实生效的 `/tdd`、`/diff`、`/term` 与动态 MCP 工具；
* **设置中枢彻底实事求是：移除假开关与假费用 (`App.vue`)**：
  - **安全防线真实说明**：重构安全沙箱页，彻底删除三行硬编码的“已开启”假状态开关，改为严谨阐述 Go 微内核物理受控沙箱、SafetyRail 高危指令拦截与敏感凭据脱敏三大不可妥协的底层硬防线；
  - **关于页面剥离假计费与假更新**：彻底删除本地字符估算的“费用估算（非账单）”与未打通自动更新逻辑的“检查更新”按钮，仅保留真实运行时元数据（版本、OS、WebView、Go、数据目录）与“导出系统诊断包”；
  - **实验特性二级收纳 (`lab`)**：将 Go AST 代码图谱与本地 Token 消耗粗略估算收纳至设置下的二级选项卡「🧪 实验特性」，并明确标注适用边界，绝不挤占主界面。

### 51. 终结固定轮次硬限制、确立 AI 自主判断任务结束与防爆安全兜底机制 (Ending Fixed Round Limits & Establishing Autonomous Completion)
* **确立“AI 自主判断任务完成”核心语义 (`internal/core/loop/llm_path.go`)**：
  - 彻底根除此前设置 24 轮人为硬上限导致复杂代码重构或 TDD 调试进行到一半被系统生硬截断的机械逻辑；
  - 明确自然交互终态：在 ReAct 执行回路中，**当大模型判断当前目标已达成（未再发起任何工具调用 `len(toolReassembler) == 0`）并直接向用户输出纯文本回复时，系统即判定该任务已由 AI 自主交付圆满完成**；
* **刚性注入自主完成契约 (`internal/core/loop/strategy.go`)**：
  - 在所有执行策略的 System Prompt 注入刚性契约：“你拥有完全自主判断任务是否达成的权利。若目标已完成请直接回复用户并不再调用工具，系统确认后闭环交付；若目标尚未达成，请自主继续调用工具推进，系统绝不会在达到固定轮次前强行截断”；
* **极端死循环防爆安全熔断兜底 (`engine.go` & `llm_path.go`)**：
  - 将微内核 `maxLLMTurns` 由 24 扩展为 100，其语义从“日常任务上限”转变为纯粹的“极端无限死循环防爆安全保险丝”，防止失控死循环消耗极端 Token；
  - 会话 TaskModel 的 `ToolBudget` 设为 0，代表“自主执行模式”，仅由真实调用的 `ToolsUsed` 计数器驱动。

### 52. 嵌套子仓库暂存防崩溃、Git Porcelain 路径清洗、工作区列表去重与采纳健壮性闭环 (Submodule Diff Defense, Empty Repo Stage Guard, Porcelain Path Sanitization & Working Tree Hygiene)
* **未提交嵌入式 Git 仓库暂存防崩溃熔断 (`app_vcs.go` & `plugins/tool/git/git_tool.go`)**：
  - 根除由于工作区包含未提交 commit 的临时嵌套 Git 目录（如测试遗留的 `testrepo/`）导致用户点击“全部采纳”执行 `git add -- <subrepo>/` 时触发 Git 致命退出码 128（`error: does not have a commit checked out`）造成批量暂存中断的严重隐患；
  - 在 `GitStage` 与 `git_tool.StageFile` 中严格注入前置探活：若目标为包含 `.git` 的子目录，前置探测 `git -C <dir> rev-parse --verify HEAD`，若尚未产生有效提交则拒绝盲目 `git add` 并返回明确的友好提示，同时捕获 Git 进程输出中包含的 `does not have a commit checked out` 规整为结构化异常；
* **Git Porcelain v2 展开与空子仓库自动过滤 (`plugins/tool/git/git_tool.go`)**：
  - 将 `GetStatus` 的命令行扩展为 `git status --porcelain=v2 -uall`，使未追踪目录自动展开为具体文件的独立条目，杜绝 Monaco 将目录误当作单文件比对的异常；
  - 在 `parsePorcelainV2` 解析器中传入 `rootDir` 环境变量，自动识别并过滤无 HEAD 提交的嵌入式空 Git 仓库目录，不向待确认改动暴露不可暂存的空目录；
* **文件路径规范化与尾部斜杠清洗 (`app_vcs.go`, `git_tool.go`, `differ.go`)**：
  - 在 `GitStage`、`GitUnstage`、`RevertFile` 中全量注入 `strings.TrimRight(path, "/\\")` 与空路径守卫，防止因尾部斜杠导致底层 Git 匹配异常；
  - 在 `internal/diff/differ.go` 的 `ComputeFileDiff` 中增加目录判定拦截 `os.Stat(absPath).IsDir()`，若为目录则安全返回错误提示，坚决阻断对目录调用 `os.ReadFile` 的 I/O 崩溃；
* **前端工作区改动列表去重与目录过滤 (`frontend/src/stores/workbench.ts`)**：
  - 修复此前 `workingTreeFiles` 在同时遍历 `working` 和 `untracked` 时将未追踪项重复显示 2 次的视觉缺陷，引入 `Set<string>` 实施严格去重；
  - 在 `pendingDiffFiles` 中通过 `!p.endsWith('/')` 过滤所有目录项，确保待审查 Diff 条仅收纳真实可比对、可暂存的物理文件；并在 `stageAllPendingDiffFilesAction` 与 `revertAllPendingDiffFilesAction` 中执行去重与去空清洗。

### 53. 插件热插拔中心、DSH 算子大盘与微内核动态拓扑一等入口设计 (Hotplug Plugin Center & DeepSeek Harness Dashboard)
* **一等公民常驻工作台入口 (`ActivityBar.vue` & `ChatCockpit.vue`)**：
  - 彻底解决主工作区缺乏热插拔算子可视化入口的架构痛点，在左侧活动栏配置专用 `🧩` 入口按钮，并联动当前开启状态；
  - 在对话顶栏右侧部署 `🧩 算子大盘 (N)` 快捷指示胶囊，与全局命令面板 `Ctrl + K`（`/hotplug`）全面贯通，支持全键盘与高频一键直达；
* **微内核算子全景大盘与大模型参数契约下钻 (`app_hotplug.go` & `HotplugDashboardModal.vue`)**：
  - 严格遵循依赖倒置与架构守卫规则，通过 `a.registry.GetTools()` 动态汇总微内核底层已装载的算子（`tool.fs`、`tool.git`、`tool.terminal`、`tool.search`、`tool.ask_user`）以及外部动态加载的 MCP 算子；
  - 提供参数契约折叠面板，直接格式化呈现算子的 JSON Schema 大模型 Function Calling 契约定义与 Mutating（写盘/执行 vs 只读）属性；
* **MCP 动态服务与 SafetyRail 防线透视**：
  - 实时反映 `mcp.Manager` 管理的 stdio / sse 活跃服务进程、算子总数与握手状态；
  - 透明化呈现 P-100 终极阻断权防线（危险系统命令拦截、工作区沙箱目录穿越隔离、API Key 凭据脱敏清洗）；
* **单点物理探活与动态热重载闭环**：
  - 支持无需重启客户端即可执行 `ReloadHotplugRegistry()`，实时同步配置并重新探测外部算子；支持对单个 Tool、Provider 或 MCP 服务发起物理探活与时延（TTFT）测量；
  - 集成 PRD §4.14 DSH 技能造物主工作台（Creator Mode），提供现场新建 Skill、编写 Rule 与挂载 MCP 的沉浸式操作闭环。

### 54. 废除三态互斥模式与进化为全自主统一 Coding Agent (Unified Autonomous Coding Agent Architecture)
* **消除范畴错误与模式选择心智摩擦**：
  - 彻底废除 `analyze`（只读）、`implement`（改代码）、`tdd`（测试）三态机械互斥单选；
  - 阐明核心架构哲学：权限控制（只读 vs 改写）与工程方法（TDD）属于正交维度，强行做成三选一导致心智割裂与体验臃肿；
  - 移除前端底部模式选择胶囊与策略切换拦截弹窗，恢复最符合开发者本能的自然语言纯净输入与全自主 Agent 执行流。
* **三层安全底座协同护航**：
  - 前置 SafetyRail 零信任拦截高危命令；
  - 写入瞬间轻量 Git 影子快照支持秒级撤销；
  - 后置 Monaco Diff 强交互视窗，由开发者通过行级分块审查牢牢掌控最终发货采纳权。

### 55. 版本号统一定标为 0.0.1 与开发测试孵化阶段工程基线 (Version Recalibration to 0.0.1 for Early Incubator Phase)
* **语义化版本 SemVer 实事求是定标**：
  - 纠正过早标记 `2.0.0` 的误导性预期，将全链路发货版本重置校准为 `0.0.1`（标识系统正处于极早期开发测试、架构加固与需求孵化阶段）；
* **跨层级强一致性全域同步**：
  - 同步 Go 微内核 `RuntimeInfo.Version` 与 Release 检查回退文本；
  - 同步 `wails.json` 的 `productVersion`、根目录与前端 `package.json` 的 `version`；
  - 同步前端 Pinia 状态 `runtimeInfo` 与 `wailsBridge` 离线兜底运行时；
  - 同步 Windows 安装向导标题、注册表 `DisplayName` / `DisplayVersion` 及打包流水线二进制产物 `Tiancode_Setup_v0.0.1.exe`；
  - 同步网络层 OAuth 刷新 `User-Agent: tiancode/0.0.1` 与 MCP 握手 `clientInfo.version`，确保整机单点版本严格一致。

### 56. 重构升级代码架构与依赖治理工作板 (Code Architecture & Dependency Governance Workbench)
* **淘汰玩具级 AST 视图与架构认知升级**：
  - 彻底移除旧版简陋的前端 24 轮 SVG 随机力导向图，建立基于 Go 官方编译前端（`go/parser`, `go/token`, `go/ast`）的现代化架构与依赖治理工作板；
  - 自动依据根目录 `go.mod` 分离工作区内部模块引用与外部依赖，实现毫秒级（<30ms）内存 AST 分析。
* **六层语义拓扑 DAG 与单向依赖防腐守卫 (Layered DAG & Architecture Rail)**：
  - 自动将工作区包归类至 `entry`、`host`、`core`、`bus`、`spec`、`tool` 六层架构；
  - 严格根据 `AGENTS.md`【铁律 7】（插件热插拔架构）实时监控单向依赖方向，高亮标红违规导入（如插件反向依赖 core），支持“仅看违规”一键过滤。
* **隐式接口契约多态匹配矩阵 (Contract Matrix)**：
  - 通过 Duck Typing 方法签名比对算法，自动将所有抽象接口（Interface）与实现结构体（Struct）进行契约匹配与覆盖率透视，完美呈现依赖倒置原则。
* **重构影响面毫秒级雷达 (Blast Radius Analysis)**：
  - 支持指定任意核心符号或结构体，秒级分析直接调用者（Direct Callers）、下游传递受影响包（Indirect Packages）以及关联需要回归的 `*_test.go` 测试套件，给出精准风险评级与重构建议。
* **双向飞轮与极简交互入口**：
  - 遵循 Warm Minimalist 规范（`#FAF8F5` 底色 + `#D96B27` 陶土橙），提供缩放平移画布与详细符号抽屉；
  - 在活动栏（`🏛️`）、对话顶栏及设置中枢提供一等快捷入口；支持一键将架构全景注入 Agent 提示词上下文，形成“认知-指导-修改-防腐”的正向工程飞轮。
* **Monorepo 多子模块自动发现、外部项目原生拾取与 Agent 防投毒物理守卫**：
  - **Monorepo / 子应用自动探测**：自动识别工作区内所有独立 `go.mod` 模块与 `cmd/` 下独立入口，支持在顶栏下拉快速下钻聚焦；
  - **外部项目原生拾取**：支持通过 Windows 原生文件夹对话框任意选取本地其他 Go 项目进行独立架构审阅，免去切换主工作区的心智打扰；
  - **Agent 防投毒物理守卫 (Context Poisoning Defense)**：在外部参考模式下，顶栏与抽屉底部的“注入 Agent”按钮自动物理禁用并提示防投毒，严禁跨项目污染主会话；
  - **瞬态自愈重置**：关闭工作板自动重置为当前主工作区，杜绝全局状态漂移。



