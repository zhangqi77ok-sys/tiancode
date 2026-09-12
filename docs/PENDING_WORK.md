# 湉码 / tiancode — 未完成功能盘点（Pending Work Audit）

> **基线**：HEAD `fad1c72` · `feat(wp-h2): 危险命令确认机制 (允许这一次) 与 UI 适配`
> **方法**：git 历史 + 全库符号检索 + 定点读源码（每条结论带 `file:line` 或 commit）
> **栈**：Wails v2 + Go 微内核 + Vue 3

## 证据边界（先读）

1. 审查环境 **`go` 不在 PATH**（`go version` 失败）。**本文不含任何「测试通过 / 失败」的运行态结论**，所有状态判定均基于**源码事实 + commit 记录**。
2. 接手第一步请在本机跑（用结果推翻本文任何一条都属正常）：
   - `go run ./tools/archcheck`（架构守卫）
   - Go 单元测试：以「点 + 斜杠 + 三连点」通配跑**根 module 全包**，再单独跑 `backend` 独立 module（根目录通配覆盖不到它）
   - `npm.cmd run build`（前端，工作目录 `frontend`）
3. `file:line` 为审查快照；上游提交后**以语义为准**。

---

## 0. 结论速览（最重要）

**已完成的部分比预期多得多，不要重做。** 逐条均有 commit 或源码证据：

| 合同 | 状态 | 证据 |
|------|------|------|
| WP-1 至 WP-10（原型缺口） | 已完成 | commit `ab62821` 起，至 `4915732`（`feat(contract): complete WP-1 to WP-10`） |
| WP-R1 探活严格 TLS | 已完成 | `e3dba39`；`internal/network/pinger.go:13` strict / `:21` loopback 两套 transport |
| WP-R2 `rm` 语义拦截 | 已完成 | `786f87f`；`plugins/rail/safety/safety_rail.go:66` `blocked by SafetyRail (semantic)` |
| WP-R3 能力元数据 `Mutating` | 已完成 | `2e14d03`；`pkg/plugin/v1/types.go:66`、`internal/core/loop/strategy.go:44`、`internal/core/loop/tools.go:30-34` |
| WP-R4 删死层 `StreamChat` | 已完成 | `e75653a` |
| WP-R6 降级清理 | 主体完成，CI 有残留 | `b3ba3f5`（`toolMap` / `maxSteps` 已从代码消失，仅剩 docs） |
| WP-H1 选择题 `ask_user` | 已完成 | `33d3043`；`app_chat.go:117 ResumeAgentChoice` |
| WP-H2 危险命令一次授权 | 已完成 | `fad1c72`；`app_chat.go:125 ResumeAgentConfirm`、`app_chat.go:303` 事件 `agent:confirm` |
| F7 发版流水线 | **实际已完成** | `.github/workflows/release.yml`（tag `v*` 触发，产出 `Tiancode_Setup_v2.0.0.exe`）—— 但 ROADMAP 未记录 |

**真正的欠债只有 3 项 + 3 份误导文档。** 详见下文。

---

## 1. 真实欠债（应做未做）

### 1.1 【P2】WP-R5 非 Windows 密钥权限加固 —— 未实现

**证据**：
- 全库检索 `0600` / `0700` **仅命中 docs**，代码中零使用。
- `internal/config/secret.go:9`：`const secretPlainPrefix = "plain:"`（非 Windows 走明文前缀）。
- 目录权限仍为 `0755`，散落多处：`internal/config/channel_store.go:38`、`extra_stores.go:58`、`extra_stores.go:103`、`paths.go:16`、`paths.go:36`、`paths.go:56`、`projects.go:28`。
- 提交历史中 `wp-r1` / `r2` / `r3` / `r4` / `r6` 俱全，**唯独 `wp-r5` 缺席**。

**影响**：Windows 发货走 DPAPI，不受影响；**Linux / macOS 下 API Key 明文落盘**，且目录世界可读。
**合同立场**：`docs/REVIEW_REMEDIATION_HANDOFF.md` 明确「仅 Windows 发货时默认延后」→ 这是**已知取舍**，非疏漏。
**若要做**：目录 `0700` + 文件 `0600`，非 Windows 单测用 `os.Stat` 断言（先红后绿）。

### 1.2 【P2】WP-R6 CI 残留 —— 部分完成

**证据**：`.github/workflows/ci.yml` 现状
- 已含 `go vet` 通配 + `go test` + archcheck + 前端 build（R6 已做）
- **仍仅 `ubuntu-latest`**：无 Windows 测试 job（DPAPI 分支在 CI 中永不编译）
- **不含 `backend/`**（双 module，根目录通配覆盖不到；`backend/` 下只有 `go.mod` + `cmd`）

**说明**：`.github/workflows/release.yml` 已提供 `windows-latest` **构建**，但它只在 push tag 时触发，**不承担测试职责**。故「CI 无 Windows 测试」仍成立。

### 1.3 【悬空需求】Checkpoint 回滚 —— 活路径零实现

**证据**：全库检索 `checkpoint` → **命中全在 `archive/` 下**：
- `archive/prototype/src/App.tsx`（Tauri / React 原型）
- `archive/src-desktop/checkpoint_service.py`（Python 服务）

活路径（`app*.go` / `internal/` / `plugins/` / `frontend/src/`）**无任何实现**。
**性质**：`docs/features/checkpoint-and-lsp.md` 提了该需求（Draft v0.1），但 `docs/V1_FEATURE_BOUNDARY_MATRIX.md` 的「明确排除」清单**既未列入也未排除** → **悬空**，需人类拍板「做」或「明确标记延后」。

---

## 2. 已声明延后 / 排除（非缺陷，勿当 Bug 修）

| 项 | 声明处 | 现状 |
|----|--------|------|
| LSP 全量语言服务器集群 | 矩阵「明确排除」+「v1.2 规划」 | `internal/lsp/` 仅 `diagnostics.go`：**正则解析 `go vet` / `tsc` / `py_compile` 输出**，**非 LSP client**（全库无 `Content-Length` / JSON-RPC 帧） |
| LSP 语义索引器（SQLite FTS5 + 调用图） | `docs/features/wp-h-lsp-semantic-indexer.md` | 未实现（且该文档栈已作废，见 §3） |
| AI 代码血缘 / 合规审计链 | `docs/features/wp-i-code-graph-and-ai-lineage.md` | 检索 `lineage` **仅命中该文档自身** → 未实现 |
| Swarm 多智能体 / PTY 多终端 / Air-Gap / OAuth 矩阵 / 技能市场 | 矩阵「明确排除」、合同「非目标」 | 不做，且**禁止**画空算子 |
| 知识图谱 / 遥测大盘 | 矩阵标实验特性 | `internal/ast/scanner.go` 存在，界面标 `[实验]` |

---

## 3. 文档债（会误导下一个 AI，建议尽快修）

### 3.1 `docs/ROADMAP.md` 状态落后
- F1 至 F6 同时出现在「正在做 / 下一波」与「完成记录」——**自相矛盾**。
- **F7 发版流水线**只在「下一波」，但 `release.yml` 已实现 → **已完成却未记录**。
- 建议：把 F1 至 F7 全部移入完成记录，并补 F7 证据。

### 3.2 `docs/features/` 三份 PRD 全部基于**已作废栈**

| 文档 | 自标状态 | 致命问题 |
|------|---------|---------|
| `checkpoint-and-lsp.md` | Draft v0.1 | 引用 `prototype/`、`pytest`、`vitest`、`/api/checkpoints`、`SessionActorManager`、`Stage Gate` |
| `wp-h-lsp-semantic-indexer.md` | 标 Ready for Implementation (v1.0) | 引用 `127.0.0.1:8010` REST、SQLite FTS5 |
| `wp-i-code-graph-and-ai-lineage.md` | 标 Ready for Implementation (v1.0) | 引用 `/api/lineage/*`、`127.0.0.1:8010` |

这些都是 **Tauri / Python / HTTP 宿主**架构产物，与合同「活路径只认 Wails + Go + Vue」**直接冲突**，且与矩阵「LSP 排除在 v1.0 外」互相矛盾。**建议：移入 `docs/archive/` 或加醒目「已作废」横幅**，否则下一个 AI 可能照它去接第二套 REST 服务（违反铁律 A1）。

---

## 4. 已知技术债 / 待人类拍板

| 项 | 证据 | 待决 |
|----|------|------|
| 探活伪造 UA | `internal/network/pinger.go` 中 `User-Agent: codex_cli_rs/0.101.0`（后接平台串） | 是否为上游网关兼容所必需？保留（集中为常量）还是移除 |
| `EngineRequest.Provider` 语义 | R6 已按 ID 排序去随机，但「按字段精确选」是否落实需复核 | 多 provider 时行为确认 |

---

## 5. 建议执行顺序（每个先写红测）

1. **P2 · 修文档债**（§3）：零风险、高收益，先做——更新 ROADMAP；给 3 份作废 PRD 加横幅或归档。
2. **P2 · WP-R5**（仅在拍板「非 Windows 也要发货」后）：红测 `TestSecretFilePerms_NonWindows`（断言 `0600` / `0700`）→ 改 `secret.go` + `internal/config/`。
3. **P2 · WP-R6 CI 残留**：新增 `windows-latest` 测试 job（至少 `go build -tags "desktop,production"`）+ `backend/` 独立测试。
4. **拍板 Checkpoint 回滚**（§1.3）：做，或明确写入矩阵「延后」。

---

## 6. 本文自检

- [x] 每条结论带 `file:line` 或 commit hash。
- [x] 未把「未运行测试」伪装成「已验证」。
- [x] 未把「已声明延后项」当成 Bug（已单列 §2）。
- [x] 未建议引入第二套 LLM 回路 / 恢复 `archive/` 栈。
- [ ] **未跑 Go 单元测试**（审查环境无 Go 工具链）—— 由接手者补齐。
