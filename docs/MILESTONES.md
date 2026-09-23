# 里程碑（MILESTONES）

> 每个里程碑有明确出口标准；全部满足才算完成。状态更新随提交进行。

## M0 脚手架 + 方案文档体系 + 守卫 —— 已完成

**范围**：legacy 归档；.gitignore 提交纪律；分层骨架与端口契约（包级文档）；arch_check R1-R4；golangci 门禁；前端骨架；docs 体系 + ADR。

**出口标准**：
- [x] 离线 `go build ./... && go vet ./... && go test ./...` 全绿
- [x] `scripts/arch_check.ps1` PASS
- [x] docs 体系入库（`ARCHITECTURE` / `CONTRACTS` / `TESTING` / `MILESTONES` / `STANDARDS` + `adr/0001~0006`）
- [x] legacy + 新 main 推送远端（远端 `main`、`legacy`、`backup-legacy-v0.10`、`backup-pre-clean-v2` 均已存在）

> 口径更正：原文写作"docs 六件套 + ADR×4"（出口标准处又写"九件"），与实际不符——
> 实际是 5 篇主文档 + 6 篇 ADR（见 `STANDARDS.md` §4 的文档目录定义）。已按实物统一。

### 补记：基建断链修复（2026-09-23）

同日排查"新环境能否真的跑通"时，发现四处**构建与门禁断链**。它们此前未被发现，是因为门禁没有载体
（见下一节 CI）。修复后补登为 M0 的出口标准：

- [x] **`frontend/dist/.gitkeep` 入库** —— 否则全新克隆缺 `frontend/dist` 目录，`go:embed all:frontend/dist`
      直接报 `pattern ... no matching files found`，`go build` / `go vet` / `go test` 三条全红（已实测复现）
- [x] **`.gitignore` 白名单改为真正的取反规则**（`!vendor/**`）—— 原先是"注释里列一份清单"，注释不生效；
      于是 `*.exe` 把 wails 必需的 `vendor/.../webview2runtime/MicrosoftEdgeWebview2Setup.exe` 挡在仓库外，
      而 `webview2installer.go` 用 `//go:embed` 无条件嵌入它，导致同样的三条命令全红
- [x] **`bin/ build/ dist/` 锚定到仓库根**（`/bin/` `/build/` `/dist/`）—— 不锚定的 `dist/` 会连
      `frontend/dist` **目录本身**一起排除，而 git 的规则是"父目录被排除时无法再取反其中的文件"，
      使 `!frontend/dist/.gitkeep` 永久失效。这正是占位文件从未入库的**真实机制**
- [x] **新增 `.gitattributes` 统一行尾** —— Git for Windows 默认 `core.autocrlf=true` 会把工作区签出为 CRLF，
      而 gofmt 只输出 LF，于是 `gofmt -l` 列出**全部 60 个** Go 文件，`STANDARDS.md` §6 的格式门禁
      在任何 Windows 开发机上都永远无法通过
- [x] **`.github/workflows/ci.yml` 落地** —— `STANDARDS.md` §6 定义的五道门禁此前**没有任何载体**：
      仓库里不存在任何 CI 配置，但 STANDARDS / arch_check.ps1 / .golangci.yml 三处都声明"由 CI 强制"

## M1 事件账本会话内核 —— 已完成

**范围**：`internal/core/session` JSONL 账本：追加（fsync）/ 重放（含断尾截断）；`internal/platform/atomicfile`。

> 范围更正：原文含"Windows 备份式替换回退"。账本自 ADR-0002 起为追加式（`O_APPEND` + fsync），
> **不再有全量改写 + rename**，该回退的实现移至 `internal/platform/atomicfile`，保护对象是渠道配置与文件写入。

**出口标准**：
- [x] C-SES-1 ~ C-SES-6 全部测试绿（TDD：先红后绿）
      —— C-SES-5 的锁定测试名更正为 `TestWriteFileAtomic_RenameConflictFallback`（见 `CONTRACTS.md` 契约变更记录）
- [x] arch_check PASS；`go test ./...` 离线全绿

## M2 流式对话环 —— 已完成（剩桌面人工验收）

**范围**：`internal/platform/openaiprovider`（流式纪律：空闲看门狗/发送逃生/EndReason）；`core/llm.ChatRuntime` 运行时抽象（ADR-0005：流前重试/流中不换渠道/超时预算）；`core/agent` ReAct 循环 + Phase 状态机；`internal/app` ChatService 四节点 Pipeline；`app/` 绑定层 + `main.go`（首次引入 wails 依赖 + vendor）；最小对话 UI（会话列表/流式气泡/中断按钮）。

**出口标准**：
- [x] C-LLM-1 ~ C-LLM-7、C-RT-1 ~ C-RT-4、C-APP-1、C-APP-2 测试绿
      —— C-RT-4 与 C-APP-2 的锁定测试此前缺失，2026-09-23 补齐（见 `CONTRACTS.md` 契约变更记录）。
      补测时发现并修复：`core/llm/runtime.go` 的中继把 ctx 取消误判为 `EndError`，
      导致用户点"中断"会看到错误而非"已取消"
- [x] httptest 模拟上游的六种故障时序全部按契约收束
- [x] 设计模式按 ADR-0005 落位（Runtime/State/Pipeline），无越界仪式
- [ ] **桌面端到端人工验收**：启动 → 发一条消息 → 流式渲染 + 终态标签正确（需人工 GUI，见 M5）
- [x] `go mod vendor` 入库，离线全量构建测试通过（依赖 `frontend/dist/.gitkeep`，见 M0 补记）

## M3 文件编辑环 —— 已完成

**范围**：fs 工具（read/write 原子写/replace 唯一性校验）；工具卡片 UI。

**出口标准**：
- [x] C-FS-1 ~ C-FS-4 测试绿
- [x] agent 可完成"读取→修改→写回"闭环（多步 ReAct + role=tool 回填），UI 显示工具卡片

## M4 命令+git 环 —— 已完成

**范围**：shell 工具（可配超时默认 120s/部分输出/TIMEOUT 标记/后台日志有界/进程树终止）；git 状态与基础操作。

**出口标准**：
- [x] C-TOOL-1 ~ C-TOOL-5 测试绿（shell：超时可配/部分输出+TIMEOUT/业务失败/后台日志有界/取消杀进程树）
- [x] agent 可完成"跑测试→读输出→修文件"闭环（shell + 只读 git 工具已装配）

## M5 打包与四环验收 —— 进行中（自动化项已过，剩人工验收）

**范围**：`wails build` Windows 单 exe；全量回归。

**出口标准**：
- [x] 全部契约测试绿（10 包，`-count=1` 新鲜执行）；arch_check PASS；golangci-lint 零告警
- [x] 新环境按 README 三命令跑通；离线构建验证（`GOPROXY=off` + vendor）
- [x] 真实上游冒烟通过（grok-4.6 流式 EndDone 收束）
- [x] **安装包流水线**（`scripts/release.ps1`：原生 Go 安装器，安装/卸载全生命周期实证通过）
- [x] **安装版可用性修复**（配置改用户级文件 + env 覆盖；数据目录固定用户级；启动失败弹框可见——ADR-0006）
- [ ] 安装版启动实证（无配置→生成模板并提示；有配置→窗口正常打开）
- [ ] 四环人工验收各一例真实任务，失败路径符合契约（待人工 GUI 验收）
