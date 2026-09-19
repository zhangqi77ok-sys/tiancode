# 湉码 / tiancode 工程文档与知识库总览 (Documentation & Knowledge Hub)

欢迎查阅 湉码 (tiancode) 官方工程文档中心。本项目遵循严格的微内核解耦、零假数据（Zero Demo Policy）、Fail-Closed 安全防护与 Warm Minimalist 人机工程学设计规范。

---

## 🧭 文档中心导航

```text
docs/
├── README.md                                  # [本文件] 文档中心导航总览
├── ARCHITECTURE_EVOLUTION.md                  # 56 次核心架构演变与工程迭代详实记录
│
├── 🏛️ 架构与协议契约
│   ├── ARCHITECTURE.md                        # 系统总体架构设计规范
│   ├── ARCHITECTURE_GO_PLUGIN_CORE.md         # Go 微内核与插件注册中心 (SPI) 规范
│   ├── TCODE_STUDIO_V2_TECHNICAL_ARCHITECTURE_AND_IPC_SPEC.md # 桌面 IPC 桥接与协议规范
│   ├── AI_IMPLEMENTATION_CONTRACT.md          # AI 实现工作包唯一施工契约 (WP-1 ~ WP-10)
│   ├── HITL_CONTRACT.md                       # 人机协同决策门禁 (Human-In-The-Loop) 契约
│   ├── REVIEW_REMEDIATION_HANDOFF.md          # 审查缺陷整改契约 (WP-R1 ~ WP-R6)
│   └── EXCEPTION_HANDLING_SPEC.md             # 异常流转与错误码规约
│
├── 📋 产品需求与功能矩阵
│   ├── PRODUCT_REQUIREMENTS_DOCUMENT.md       # 完整产品需求文档 (PRD)
│   ├── PRODUCT_ONE_PAGER.md                   # 产品一页纸愿景与核心价值
│   ├── V1_FEATURE_BOUNDARY_MATRIX.md          # V1 版本特性边界与交付验收矩阵
│   └── ROADMAP.md                             # 迭代演化路线图
│
├── 🎨 视觉与人机工程学
│   ├── UI_DESIGN_SPEC.md                      # Warm Minimalist 视觉体系与配色规范
│   └── UI_UX_DESIGN_SPEC_NEXTGEN.md           # 16:9 人机工学工作台与单焦点切换规范
│
└── 📚 核心工程知识库 (knowledge/)
    ├── README.md                              # 知识库索引导航
    └── 15-54 文档                             # 40 篇现行 Wails v2 + Go 微内核 + Vue 3 技术攻坚实录
```

---

## 🏛️ 一、核心架构与协议契约

| 文档名称 | 核心主题 | 说明 |
| :--- | :--- | :--- |
| [`ARCHITECTURE.md`](./ARCHITECTURE.md) | 总体架构 | 阐述 Wails v2 + Go 微内核 + Vue 3 桌面端核心架构与分层设计 |
| [`ARCHITECTURE_GO_PLUGIN_CORE.md`](./ARCHITECTURE_GO_PLUGIN_CORE.md) | Go 插件式微内核 | 强类型 SPI 契约、分段锁注册中心 (`host.Registry`) 与看门狗 |
| [`TCODE_STUDIO_V2_TECHNICAL_ARCHITECTURE_AND_IPC_SPEC.md`](./TCODE_STUDIO_V2_TECHNICAL_ARCHITECTURE_AND_IPC_SPEC.md) | IPC 通信与契约 | 前端与 Go 后端 Wails IPC 桥接、事件流传输与状态同步规约 |
| [`AI_IMPLEMENTATION_CONTRACT.md`](./AI_IMPLEMENTATION_CONTRACT.md) | AI 施工合同 | 编码工作包 (WP-1 ~ WP-10) 的唯一交付标准与完成度定义 |
| [`HITL_CONTRACT.md`](./HITL_CONTRACT.md) | 人机决策门禁 | 关键改动审查、文件采纳、高危命令授权的阻断契约 |
| [`REVIEW_REMEDIATION_HANDOFF.md`](./REVIEW_REMEDIATION_HANDOFF.md) | 缺陷整改合同 | 针对安全、进程生命周期与协议一致性的专项整改方案 |
| [`EXCEPTION_HANDLING_SPEC.md`](./EXCEPTION_HANDLING_SPEC.md) | 异常流转规范 | 四维人话收尾机制、Fail-Closed 与错误码分类规约 |

---

## 📋 二、产品需求与功能矩阵

| 文档名称 | 核心主题 | 说明 |
| :--- | :--- | :--- |
| [`PRODUCT_REQUIREMENTS_DOCUMENT.md`](./PRODUCT_REQUIREMENTS_DOCUMENT.md) | 完整 PRD | 详细阐述产品愿景、交互逻辑、功能定义与业务边界 |
| [`V1_FEATURE_BOUNDARY_MATRIX.md`](./V1_FEATURE_BOUNDARY_MATRIX.md) | 特性边界矩阵 | 诚实界定可用（🟢）、半成品（🟡）与实验特性（🔴），拒绝假交付 |
| [`ROADMAP.md`](./ROADMAP.md) | 路线演进图 | 版本迭代计划与长期规划（多 Agent 协作流、磁盘动态插件等） |
| [`TECHNICAL_AND_PRD_REPORT.md`](./TECHNICAL_AND_PRD_REPORT.md) | 架构与 PRD 报告 | 详实梳理前后端职责边界与生产级功能落地标准 |

---

## 🎨 三、视觉与人机工程学规范

| 文档名称 | 核心主题 | 说明 |
| :--- | :--- | :--- |
| [`UI_DESIGN_SPEC.md`](./UI_DESIGN_SPEC.md) | 视觉设计系统 | Warm Cream 调色盘（`#FAF8F5` / `#F4EFEA` / `#D96B27` / `#1E1C1A`） |
| [`UI_UX_DESIGN_SPEC_NEXTGEN.md`](./UI_UX_DESIGN_SPEC_NEXTGEN.md) | 交互与布局 | 16:9 桌面工作台比例、单焦点聚合切换（对话/双栏/代码）与弹窗铁律 |

---

## 🚀 四、架构演进历程 (52 次迭代)

详见 [`ARCHITECTURE_EVOLUTION.md`](./ARCHITECTURE_EVOLUTION.md)，包含：
- **阶段一 (迭代 1 - 8)**：单一执行内核收敛、四维人话收尾、Session 任务模型、「继续」无损接续、双栈 TDD、控件诚实正名与零假数据；
- **阶段二 (迭代 9 - 17)**：前后端 Wails v2 融合、流式中断、受控沙箱与 Git 快照、Monaco 虚拟化 Diff、单文件安装向导；
- **阶段三 (迭代 28 - 30)**：MCP stdio 管道接入、LSP 编译器毫秒级自愈、跨语言栈自动识别；
- **阶段四 (迭代 31 - 42)**：前十大关键缺陷歼灭、进程树级联销毁、目录防穿越、盘符归一化、HTTP 连接池复用、Git 空格文件名修复；
- **阶段五 (迭代 43 - 47)**：空输出网关容错、RevertFile 防误删、目录防覆盖覆盖、无 HEAD 仓库已暂存安全撤销、已删除文件 Diff 展示；
- **阶段六 (迭代 48 - 52)**：工作区动态重绑定、Tab 内存草稿持久化、全仓真检索视窗、活动栏五大极简入口收敛、AI 自主终态判断与防脱缰熔断器、嵌套子仓库防崩与 Porcelain 路径清洗。

---

## 📚 五、核心工程知识库索引 (docs/knowledge/)

> 本知识库依据 `AGENTS.md`【铁律 6】强制设立：深入剖析底层机制、相关技术规范，给出实测可验证的标准解决方案与避坑指南。

| 序号 | 知识点 / 技术议题 | 领域分类 | 核心关注点 | 知识点文档 |
| :---: | :--- | :--- | :--- | :--- |
| **15** | **Wails v2 生产级编译、沉浸窗体与纯 Go 安装向导** | 桌面内核 / 原生分发 | `-tags "desktop,production"` 编译、无边框沉浸窗口、纯 Go 资源内嵌向导 | [`15-wails-v2-production-build-and-frameless-installer.md`](knowledge/15-wails-v2-production-build-and-frameless-installer.md) |
| **16** | **Git 行级 Unified Diff 解析与 Hunk Cherry-Pick 采纳** | 代码审查 / GitOps | Unified Diff 状态机分块、`git apply --cached` 暂存与反向还原 | [`16-monaco-unified-diff-and-hunk-cherry-pick.md`](knowledge/16-monaco-unified-diff-and-hunk-cherry-pick.md) |
| **17** | **Windows CREATE_NO_WINDOW 流式终端管道与事件流** | 进程控制 / 终端 | `0x08000000` 零黑框、并发双管道非阻塞流式推流与进程取消 | [`17-controlled-streaming-terminal-and-no-window-pty.md`](knowledge/17-controlled-streaming-terminal-and-no-window-pty.md) |
| **18** | **MCP 跨进程 Stdio 协议传输与 ReAct 动态调度** | 扩展生态 / MCP | Anthropic JSON-RPC 2.0 握手、外部进程生命周期与算子动态路由 | [`18-mcp-protocol-stdio-lifecycle-and-react-dispatch.md`](knowledge/18-mcp-protocol-stdio-lifecycle-and-react-dispatch.md) |
| **19** | **LSP 编译器毫秒级语法诊断与 MCP 前端治理看板** | 编译器 / 自愈 / UI | 多语言轻量语法诊断探针、落盘自愈注入与 MCP 实时探活面板 | [`19-lsp-compiler-diagnostics-and-mcp-dashboard.md`](knowledge/19-lsp-compiler-diagnostics-and-mcp-dashboard.md) |
| **20** | **工作区技术栈自适应探测与自主 ReAct 收敛状态机** | 技术栈感知 / 状态机 | 多语言特征扫描识别、Zero Tool Calls 自然收敛准则与流动执行卡片 | [`20-language-agnostic-stack-detection-and-natural-react-loop.md`](knowledge/20-language-agnostic-stack-detection-and-natural-react-loop.md) |
| **21** | **安装向导自定义目录解析、原生拾取器与自清理闭环** | 桌面分发 / 原生交互 | 命令行参数统一解析、WinForms 原生目录拾取、卸载器自删除闭环 | [`21-installer-custom-directory-and-folder-picker.md`](knowledge/21-installer-custom-directory-and-folder-picker.md) |
| **22** | **桌面端纯净零假数据治理与模板级渲染性能优化** | 前端架构 / 性能优化 | 全域假数据清空、纯净空状态设计、LRU Map Markdown 渲染防雪崩 | [`22-zero-demo-empty-states-and-ui-performance-optimization.md`](knowledge/22-zero-demo-empty-states-and-ui-performance-optimization.md) |
| **23** | **后端持久化假数据根除与 Go 原生单文件打包构建** | 数据治理 / 安装器 | 铲除 NewStore 自愈假数据、动态标签对齐、单文件安装向导构建 | [`23-elimination-of-persisted-mock-sessions-and-native-installer-pipeline.md`](knowledge/23-elimination-of-persisted-mock-sessions-and-native-installer-pipeline.md) |
| **24** | **核心系统前十大关键缺陷歼灭与桌面微内核加固** | 架构加固 / 缺陷治理 | 凭据零泄漏、Fail-Closed 契约、递归文件树、空 Content 协议修复 | [`24-top-10-critical-bugs-eradication-and-architecture-hardening.md`](knowledge/24-top-10-critical-bugs-eradication-and-architecture-hardening.md) |
| **25** | **Windows 进程树生命周期隔离与未追踪文件 Diff 适配** | 进程控制 / GitOps | `taskkill /F /T` 树杀、Untracked 文件 Diff 适配、沙箱盘符归一化 | [`25-process-tree-isolation-untracked-diff-and-ui-modals.md`](knowledge/25-process-tree-isolation-untracked-diff-and-ui-modals.md) |
| **26** | **文件树与会话防穿越守卫、编译诊断无网络阻断** | 访问控制 / 性能防护 | 会话 ID 白名单、文件树沙箱前缀校验、`--no-install` 防网络阻塞 | [`26-path-traversal-defense-and-session-state-hygiene.md`](knowledge/26-path-traversal-defense-and-session-state-hygiene.md) |
| **27** | **推理流中断、无头静默卸载与配置原子写** | 智能体控制 / 数据安全 | 全链路推理取消上下文、无头静默自删除、配置临时文件原子落盘 | [`27-stream-cancellation-silent-uninstall-and-extra-stores-purging.md`](knowledge/27-stream-cancellation-silent-uninstall-and-extra-stores-purging.md) |
| **28** | **任意文件删除防御、会话原子落盘与用量核算准确性** | 安全防御 / 遥测核算 | 文件回滚防沙箱逃逸、会话原子写防撕裂、Token 真实成本格式化修复 | [`28-arbitrary-file-deletion-guard-session-atomic-write-and-telemetry-accuracy.md`](knowledge/28-arbitrary-file-deletion-guard-session-atomic-write-and-telemetry-accuracy.md) |
| **29** | **动态工作区热切换、稀疏工具调用治理与长思考流扩容** | 架构扩展 / 协议防御 | 工作区热切换与原生拾取、稀疏 tool_calls 排序遍历、10MB 思考流缓冲 | [`29-workspace-switching-sparse-tool-calls-and-process-tree-safety.md`](knowledge/29-workspace-switching-sparse-tool-calls-and-process-tree-safety.md) |
| **30** | **MCP 外部握手并发优化、守护协程防泄漏与路径空格** | 协议并发 / CLI 边界 | 握手移出全局写锁、终端守护协程退出通道、Git 空格路径保护 | [`30-mcp-concurrency-goroutine-leak-and-status-path-hygiene.md`](knowledge/30-mcp-concurrency-goroutine-leak-and-status-path-hygiene.md) |
| **31** | **插件分段锁热替换、驱动器盘符归一化与取消保护** | 架构扩展 / 并发时序 | 注册中心原子替换接口、盘符大小写归一化、单调递增任务序号防冲空 | [`31-plugin-hot-reload-drive-normalization-and-concurrency-cancel-guard.md`](knowledge/31-plugin-hot-reload-drive-normalization-and-concurrency-cancel-guard.md) |
| **32** | **MCP 悬挂通道空指针防御、代码审计 OOM 熔断与 HTTP 复用**| 内存安全 / 网络性能 | StdioClient 关闭时 pending channel 防空指针、5MB 审计大文件熔断 | [`32-mcp-pending-nil-defense-audit-oom-and-http-connection-pooling.md`](knowledge/32-mcp-pending-nil-defense-audit-oom-and-http-connection-pooling.md) |
| **33** | **未追踪代码块安全丢弃与网络探活连接池治理** | 代码审查 / 网络性能 | 未追踪单文件 Hunk 丢弃物理回退、网络长连接池复用与本地协议适配 | [`33-untracked-hunk-discard-drive-letter-normalization-and-pinger-pooling.md`](knowledge/33-untracked-hunk-discard-drive-letter-normalization-and-pinger-pooling.md) |
| **34** | **进程树主动取消机制、工具调用 ID 守卫与 Git 变更治理** | 进程控制 / 协议合规 | `cmd.Cancel` 树杀防孤儿、`tool_call_id` 协议保全、Working Tree 真实映射 | [`34-process-tree-cancel-tool-call-id-and-git-working-tree-hygiene.md`](knowledge/34-process-tree-cancel-tool-call-id-and-git-working-tree-hygiene.md) |
| **35** | **Git 源码管理闭环绑定、TDD 进程树熔断与 Differ 容错** | 交互闭环 / GitOps | 真实提交驱动、TDD 孤儿进程树销毁、负向变更已删除文件 Diff 容错 | [`35-git-source-control-binding-tdd-process-tree-and-differ-resilience.md`](knowledge/35-git-source-control-binding-tdd-process-tree-and-differ-resilience.md) |
| **36** | **Git 分支检出双横杠陷阱、MCP Stderr 死锁与流式隔离** | 命令行协议 / 进程 IO | 移除 `git checkout --` 恢复分支切换、MCP Stderr 异步非阻塞排水防死锁 | [`36-git-checkout-double-dash-mcp-stderr-pipe-and-cancellation-isolation.md`](knowledge/36-git-checkout-double-dash-mcp-stderr-pipe-and-cancellation-isolation.md) |
| **37** | **会话更新时序保序、Windows 设备保留名防御与凭据清洗** | 数据一致性 / 系统兼容 | 会话 `UpdatedAt` 降序保序、Windows 设备保留字（CON/NUL/AUX）拦截 | [`37-session-sorting-windows-reserved-names-and-zero-demo-hardening.md`](knowledge/37-session-sorting-windows-reserved-names-and-zero-demo-hardening.md) |
| **38** | **工具空输出防御、RevertFile 撤销防误删与快照时间戳** | 协议安全 / 数据防丢 | 工具空输出保全防 400 崩溃、无 HEAD 仓库撤销防误删已追踪文件 | [`38-empty-tool-content-defense-revert-safety-and-event-cleanup.md`](knowledge/38-empty-tool-content-defense-revert-safety-and-event-cleanup.md) |
| **39** | **目录防覆盖防御、Git 暂存区感知提交与 MCP 锁优化** | 存储安全 / GitOps | `AtomicWriteFile` 覆盖目录前置拦截、暂存区感知提交防覆盖选择 | [`39-directory-overwrite-defense-staged-git-commit-and-mcp-lock.md`](knowledge/39-directory-overwrite-defense-staged-git-commit-and-mcp-lock.md) |
| **40** | **Git Porcelain v2 重命名解析修复与无 HEAD 仓库安全撤回** | GitOps / 并发解耦 | 重命名偏移与新旧路径颠倒修复、无 HEAD 仓库撤销已暂存降级通道 | [`40-git-rename-porcelain-no-head-revert-and-staged-drawer.md`](knowledge/40-git-rename-porcelain-no-head-revert-and-staged-drawer.md) |
| **41** | **无 HEAD 仓库增改 Diff 修正与大小写安全审计激活** | GitOps / 安全审计 | 无 HEAD 仓库 AM 状态 Diff 准确识别、安全审计双向大小写归一化 | [`41-nohead-diff-case-insensitive-audit-and-unstage-fastpath.md`](knowledge/41-nohead-diff-case-insensitive-audit-and-unstage-fastpath.md) |
| **42** | **HTTP 引擎空指针熔断、Git 分支全量注入防御与 Diff 状态机**| 内存安全 / 安全防御 | 未初始化前置熔断防崩溃、Git 分支名注入全字符集拦截、已删除文件 Diff | [`42-http-nil-engine-branch-sanitize-and-diff-deletion.md`](knowledge/42-http-nil-engine-branch-sanitize-and-diff-deletion.md) |
| **43** | **工作区搜索算子架构、双栈 TDD 真实探测与编辑器多页签** | 检索算子 / TDD / Tab | 原生 Go grep/find 算子、Go+npm 双栈真实测试探测、Monaco 标签页 | [`43-workspace-search-plugin-dual-stack-tdd-and-editor-tabs.md`](knowledge/43-workspace-search-plugin-dual-stack-tdd-and-editor-tabs.md) |
| **44** | **工作区动态重绑定、Tab 内存持久化与文件树按需懒加载** | 热插拔 / 状态机 / 性能 | 切换工作区重载 searchtool、Tab 内存草稿持久化、1 层受控异步懒加载 | [`44-workspace-rebind-tab-persistence-dual-stack-tdd-and-lazy-file-tree.md`](knowledge/44-workspace-rebind-tab-persistence-dual-stack-tdd-and-lazy-file-tree.md) |
| **45** | **开发者全局检索打通、策略跳过守卫与 V1 边界矩阵对齐** | 工作区检索 / 策略状态机 | 开发者专属 grep/find 视窗、策略跳过坚守只读安全、V1 边界矩阵对齐 | [`45-human-workspace-search-strategy-guard-and-v1-matrix-alignment.md`](knowledge/45-human-workspace-search-strategy-guard-and-v1-matrix-alignment.md) |
| **46** | **剥离形式主义外壳、主界面五大极简入口与设置中枢真实化** | UI去伪存真 / 极简活动栏 | 仅留 5 大核心入口（对话/文件/Git/终端/设置）、纯粹终端、移除假开关 | [`46-de-bloating-ui-entries-and-honest-settings-hub.md`](knowledge/46-de-bloating-ui-entries-and-honest-settings-hub.md) |
| **47** | **终结固定轮次硬限制、确立 AI 自主判断结束与防爆兜底** | 自主执行回路 / 终态决策 | 废止 24 轮生硬截断、0 工具调用即自主交付完成、100 轮防爆保险丝 | [`47-ai-autonomous-task-completion-and-runaway-safety-fuse.md`](knowledge/47-ai-autonomous-task-completion-and-runaway-safety-fuse.md) |
| **48** | **控件错名治理、防脱缰智能熔断器与 TDD 结构化失败提取** | 控件诚实正名 / 智能熔断器 | 正名 Git Stash / 文件与编辑器；3 重复/3 错误熔断；双栈 TDD 失败清单置顶 | [`48-control-naming-honesty-and-runaway-circuit-breakers.md`](knowledge/48-control-naming-honesty-and-runaway-circuit-breakers.md) |
| **49** | **多协议网关、OAuth 2.0 刷新机制与 new-api 单框体验对齐** | 模型网关 / OAuth 2.0 | Google RT 绑定 client_id 原理、new-api 单框接纳 JSON、自动嗅探解构 | [`49-multi-protocol-gateway-oauth-refresh-and-newapi-alignment.md`](knowledge/49-multi-protocol-gateway-oauth-refresh-and-newapi-alignment.md) |
| **50** | **嵌套子仓库暂存防崩溃、Git Porcelain 清洗与去重健壮性** | GitOps / Submodule防崩溃 | 嵌套未提交 Git 目录 Exit 128 熔断、Porcelain v2 尾斜杠清洗与去重 | [`50-submodule-diff-defense-empty-repo-stage-guard-and-git-porcelain-hygiene.md`](knowledge/50-submodule-diff-defense-empty-repo-stage-guard-and-git-porcelain-hygiene.md) |
| **51** | **插件热插拔中心、DSH 算子大盘与微内核动态拓扑一等入口设计** | 插件热插拔 / DSH 算子大盘 | 活动栏常驻一等入口、微内核算子直查、JSON Schema 展开、单点物理探活与 Creator 模式 | [`51-hotplug-plugin-center-and-dsh-operator-dashboard.md`](knowledge/51-hotplug-plugin-center-and-dsh-operator-dashboard.md) |
| **52** | **从三态互斥到全自主统一 Coding Agent 架构演进** | 统一 Coding Agent / 意图自适应 / Monaco Diff | 彻底废除三态互斥模式与阻断弹窗、确立单一全自主 Coding Agent、自然语言意图驱动、Monaco Diff 审核把关 | [`52-unified-autonomous-coding-agent-architecture.md`](knowledge/52-unified-autonomous-coding-agent-architecture.md) |
| **53** | **版本号校准为 0.0.1 与开发测试孵化阶段工程基线** | 语义化版本 SemVer / 0.0.1基线 / 孵化期预期管理 | 全链路发货版本校准为 0.0.1，真实呈现开发测试与需求孵化阶段；同步 Wails、Go、Node、前端、安装器及协议握手强一致性 | [`53-version-recalibration-to-early-incubator-phase.md`](knowledge/53-version-recalibration-to-early-incubator-phase.md) |
| **54** | **从玩具级 Go AST 到现代代码架构与依赖治理工作板** | 架构分析 / 依赖倒置 / 影响面雷达 | 淘汰简陋24轮SVG力导向图，深度解析 Go AST；6 层语义拓扑 DAG、契约多态矩阵、重构影响面雷达与铁律 7 单向依赖防腐守卫 | [`54-code-architecture-and-dependency-governance-workbench.md`](knowledge/54-code-architecture-and-dependency-governance-workbench.md) |

---

## 🛠️ 六、文档维护规范

依据 `AGENTS.md`【铁律 4】与【铁律 6】：
1. **架构强同步**：凡产生重大代码重构或核心机制演进，必须同步更新本目录中的架构规范与根目录 `README.md`；
2. **知识点强制沉淀**：解决关键技术攻坚、环境陷阱、死锁或高频报错后，必须在 `docs/knowledge/` 归档标准四段论文档并同步更新本索引表。
