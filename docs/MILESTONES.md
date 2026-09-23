# 里程碑（MILESTONES）

> 每个里程碑有明确出口标准；全部满足才算完成。状态更新随提交进行。

## M0 脚手架 + 方案文档体系 + 守卫 —— 进行中

**范围**：legacy 归档；.gitignore 提交纪律；分层骨架与端口契约（包级文档）；arch_check R1-R4；golangci 门禁；前端骨架；docs 六件套 + ADR×4。

**出口标准**：
- [x] 离线 `go build ./... && go vet ./... && go test ./...` 全绿
- [x] `scripts/arch_check.ps1` PASS
- [ ] docs 体系九件入库（本文档体系）
- [ ] legacy + 新 main 推送远端（网络恢复后补推）

## M1 事件账本会话内核 —— 未开始

**范围**：`internal/core/session` JSONL 账本：追加（fsync）/ 重放（含断尾截断）/ Windows 备份式替换回退；`internal/platform/atomicfile`。

**出口标准**：
- [ ] C-SES-1 ~ C-SES-6 全部测试绿（TDD：先红后绿）
- [ ] arch_check PASS；`go test ./...` 离线全绿

## M2 流式对话环 —— 未开始

**范围**：`internal/platform/openaiprovider`（流式纪律：空闲看门狗/发送逃生/EndReason）；`core/llm.ChatRuntime` 运行时抽象（ADR-0005：流前重试/流中不换渠道/超时预算）；`core/agent` ReAct 循环 + Phase 状态机；`internal/app` ChatService 四节点 Pipeline；`app/` 绑定层 + `main.go`（首次引入 wails 依赖 + vendor）；最小对话 UI（会话列表/流式气泡/中断按钮）。

**出口标准**：
- [ ] C-LLM-1 ~ C-LLM-7、C-RT-1 ~ C-RT-4、C-APP-1、C-APP-2 测试绿
- [ ] httptest 模拟上游的六种故障时序全部按契约收束
- [ ] 设计模式按 ADR-0005 落位（Runtime/State/Pipeline），无越界仪式
- [ ] 桌面可启动，端到端发一条消息：流式渲染 + 终态标签正确
- [ ] `go mod vendor` 入库，离线全量构建测试通过

## M3 文件编辑环 —— 未开始

**范围**：fs 工具（read/write 原子写/replace 唯一性校验）；工具卡片 UI。

**出口标准**：
- [x] C-FS-1 ~ C-FS-4 测试绿
- [x] agent 可完成"读取→修改→写回"闭环（多步 ReAct + role=tool 回填），UI 显示工具卡片

## M4 命令+git 环 —— 未开始

**范围**：shell 工具（可配超时默认 120s/部分输出/TIMEOUT 标记/后台日志有界/进程树终止）；git 状态与基础操作。

**出口标准**：
- [x] C-TOOL-1 ~ C-TOOL-5 测试绿（shell：超时可配/部分输出+TIMEOUT/业务失败/后台日志有界/取消杀进程树）
- [x] agent 可完成"跑测试→读输出→修文件"闭环（shell + 只读 git 工具已装配）

## M5 打包与四环验收 —— 未开始

**范围**：`wails build` Windows 单 exe；全量回归。

**出口标准**：
- [x] 全部契约测试绿（10 包，`-count=1` 新鲜执行）；arch_check PASS；golangci-lint 零告警
- [x] 新环境按 README 三命令跑通；离线构建验证（`GOPROXY=off` + vendor）
- [x] 真实上游冒烟通过（grok-4.6 流式 EndDone 收束）
- [x] **安装包流水线**（`scripts/release.ps1`：原生 Go 安装器，安装/卸载全生命周期实证通过）
- [x] **安装版可用性修复**（配置改用户级文件 + env 覆盖；数据目录固定用户级；启动失败弹框可见——ADR-0006）
- [ ] 安装版启动实证（无配置→生成模板并提示；有配置→窗口正常打开）
- [ ] 四环人工验收各一例真实任务，失败路径符合契约（待人工 GUI 验收）
