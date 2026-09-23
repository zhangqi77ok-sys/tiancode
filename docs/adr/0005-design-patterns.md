# ADR-0005：设计模式的取舍——只钉在真实变化点上

日期：2026-09-23 ｜ 状态：已接受

## 背景

评审中指出新骨架"没有用到设计模式"，参照 dsh-java（策略树/责任链、Runtime 抽象、工厂、Bridge）。
本 ADR 记录取舍：**模式的价值来自钉在真实的"变化点"上**；为模式而模式正是旧 tiancode 与
dsh-java §14 自认教训（无界线程池/O(n²)）的共同病根——复杂度还债。

## 决策：采用（含 M0 已有的潜伏模式）

| 模式 | 位置 | 变化点（为什么值得） | 引入里程碑 |
| --- | --- | --- | --- |
| 端口-适配器（六边形） | `core/*Port` + `internal/platform` | 供应商/工具/存储皆可替换 | M0（已落地） |
| 事件溯源 + Repository | `core/session` 账本 | 崩溃恢复语义 + 审计 | M1 |
| **Runtime 抽象（Strategy+Facade）** | `core/llm.ChatRuntime` | 渠道选择/重试策略/超时预算——多渠道时 agent 零改动；借 new-api"流前重试、流中不换"纪律 | M2 |
| **State（状态机）** | `core/agent` Phase：Idle/Running/Cancelled | 取消正确性（C-APP-2）天然需要显式状态迁移 | M2 |
| **Pipeline（责任链轻量版）** | `internal/app` ChatService：ResolveSession→Dispatch→StreamRelay→Persist | 步骤显式路由；分支节点 ≥3 时升级为策略树 | M2 |
| Registry | 工具注册表 | 工具集增长 | M3 |
| Observer | 账本事件 → Wails 事件桥 → UI | 流式渲染与终态通知 | M2 |

## 决策：明确不用（防"手痒再加"）

| 模式 | 不用的理由 |
| --- | --- |
| Decorator/Hook 拦截链 | 旧 astguard 误杀正常写入的元凶；拦截型护栏 MVP 一律不进 |
| Bridge（插件双运行时） | 插件系统已 YAGNI（见需求澄清结论 3） |
| CQRS 读写分离 | 单用户工具，读写分离是负资产 |
| dsh-java 式 AbstractStrategyRouter 全套机械 | MVP 期多数节点单分支，机械先于需求；Pipeline 节点 ≥3 分支时按本 ADR 复评 |

## 准入判据（未来加模式前自问）

1. 是否存在 ≥2 个真实实现或可预见的具体变化？
2. 该变化点是否已造成过事故或返工？
3. 去掉该模式，变化发生时改动是否可控（≤1 个文件）？
三者皆"是"才引入，且必须更新本 ADR 变更记录。

## 变更记录

- 2026-09-23 初版：Runtime/State/Pipeline 纳入 M2；Hook/Bridge/CQRS/全套策略树明确不用。
