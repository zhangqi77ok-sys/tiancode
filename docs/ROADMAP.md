# 湉码工程任务板

原则：**不做 Demo**。没有真实渠道就不推理；没有工作区就不假装有仓库；没有技能就不注入。每完成一项独立功能就 `git push`。

活路径：Wails v2 + Go 微内核 + Vue 3（`app_*.go` / `internal/` / `frontend/src`）。

**后续 AI 只认这份施工合同（原型缺口工作包 WP-1…WP-10）**：[`docs/AI_IMPLEMENTATION_CONTRACT.md`](./AI_IMPLEMENTATION_CONTRACT.md)

**安全/质量整改（合同补充卷 WP-R1…R6）**：[`docs/REVIEW_REMEDIATION_HANDOFF.md`](./REVIEW_REMEDIATION_HANDOFF.md)

**人机协同（对话中选择题 / 危险命令允许一次 WP-H1…H2）**：[`docs/HITL_CONTRACT.md`](./HITL_CONTRACT.md)

## 正在推进 / 迭代维护（聚焦桌面稳定性与主旅程体验）

| ID | 议题 | 目标 | 验收 |
|----|------|------|------|
| M1 | 生产级稳定性加固 | 持续监控 Windows 原生桌面的运行时健壮性与内存占用 | 长时间高负荷推理与多文件 Monaco 审查零崩溃 |
| M2 | CI 自动化双平台验证 | 维护 Windows + Linux 持续集成矩阵（含 DPAPI 与原生标签构建） | GitHub Actions 双平台持续通过 |
| M3 | 用户反馈与缺陷修复 | 针对真实开发者反馈的高频问题进行敏捷迭代修复 | 按照 SDD+TDD 规范持续保证单测覆盖率 100% |

## 明确不做 / 暂缓项（聚焦核心编码主旅程）

- **暂缓（P2 Could）**：Swarm 复杂多 Agent 运行时、技能市场、OAuth 矩阵、知识图谱力导向图。主旅程稳定前不做，避免稀释产品焦点。
- **严禁事项**：
  - 严禁在没有多 Agent 运行时之前画 Swarm 空算子；
  - 严禁把 `archive/` 里的 Tauri/React/Python/HTML 接回主路径；
  - 严禁预置假会话、假插件、假指标等任何 Demo 数据；
  - 严禁堆砌 PRD 文档而不更新现行代码与规格。

## 最近完成记录 (P0 / P1 核心修复与微内核收敛)

- **P0-1 任务必须有结尾**：触顶（24轮上限阶段总结并提示发「继续」）、取消（保留内容并显式人话通知）、上游报错（解析HTTP 400/401/429/500为人话排查指引）、空结束（输出防御人话提示），彻底消除「突然没了」；
- **P0-2 审查任务先地图再下钻**：在 `StrategyAnalyze` 注入「先看地图（顶层结构+关键入口配置），再定靶向下钻」铁律，严禁盲目扫描全库；
- **P0-3 「继续」精准接续**：引入 Session 挂载的最小任务模型 `TaskModel`，当用户发送「继续」时无损承接既定目标与未完成项，禁止推翻重来；
- **P0-4 现行架构图收敛**：对外只承认 Wails + Go + Vue 3，彻底重写 `docs/ARCHITECTURE.md`，历史栈一律标归档；
- **P1-1 改文件默认出 Diff**：工具写盘后自动调出 Monaco Diff 工作区，跟踪 `PendingDiffFiles`，开发者点击采纳或放弃才算完成；
- **P1-2 TDD 失败状态流转**：TDD 测试未通过时，任务模型状态保持为 `tdd_failed`，严禁宣称任务完成；
- **P1-3 项目宪法顶栏显式可见**：在对话顶栏常驻展示 `📜 项目宪法`（启用的规约与技能统计），点击可展开完整条文；
- **P1-4 MCP 仅承诺 stdio**：内核与界面统一标准 JSON-RPC 2.0 stdio 通信，无假 SSE 承诺；
- **架构收敛**：执行内核合并为单一直接执行回路，清理冗余第二套循环；表现层收敛单一真实源 `workbench.ts`，清理未接线孤儿 Store。

## 完成记录

- 仓库改名 tiancode、单内核、SafetyRail、密钥加密、`~/.tiancode`
- Vue 壳拆分、`app.go` 拆文件、归档死栈、Windows 构建脚本
- F1 Fail-closed：无渠道不填假 OpenAI 地址；模型列表只来自渠道；抽屉用真实工作区名
- F2 Skill：启用技能的 `prompt` 注入 system prompt；保存不再误写 `content`
- F3 `/test` `/tdd` 跑真实 TDD；`/diff` 打开真实 Git 抽屉（未连接内核则报错，不假装 main 分支）
- F4 ListModels / 拉取上游：无 Key 报错，只返回网关真实 `/models`，去掉内置 gpt-4o 目录
- F5 桌面聊天 Init 已注册 Provider 再 StreamChat，不再绕过插件走裸 `llm.StreamChat`
- F6 对话默认只渲染最近 80 条，可一键展开全文（全量仍落盘）
- F7 发版流水线：构建 `.github/workflows/release.yml`，打 tag `v*` 自动产出 Windows 独立安装包 `Tiancode_Setup_v0.0.1.exe`
- WP-R1 至 WP-R6 安全整改闭环：严格 TLS 探活过滤、`rm` 语义拆分拦截、`Mutating` 工具能力元数据、删除旧层死代码、非 Windows 严格权限加固（0700/0600）与 Windows CI 构建支持
- WP-H1 与 WP-H2 人机决策协同（HITL）：实现 `ask_user` 对话中途单选卡片暂停与 `ResumeAgentChoice` 唤醒机制、危险系统命令拦截与 `ResumeAgentConfirm` 一次性显式授权弹窗
- 会话模型：按工作区隔离；空草稿不落盘；标题取首条用户消息；列表不出现空会话
- 原型树：多项目折叠 + 项目下会话分支；打开项目；点会话切换工作区
- 修复桌面白屏：Vite `base: './'`，避免 Wails 加载 `/assets` 404
- 修复启动报错：workbench 误调用未导入的 `watch`/`store`，启动失败会整页替换成红字；Git 非仓库不再当致命错误
