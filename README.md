# tiancode

个人 Windows 桌面 AI 编程智能体（从零重建版）。第一版只做核心四环，并做到**失败也一致**：
对话流式、文件编辑、命令+git、会话恢复。

- 架构与分层：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
- 行为契约：[docs/CONTRACTS.md](docs/CONTRACTS.md)
- 工程规范（提交/分层/注释/文档）：[docs/STANDARDS.md](docs/STANDARDS.md)
- 里程碑与验收：[docs/MILESTONES.md](docs/MILESTONES.md)

## 快速开始（新环境 5 分钟）

前置：Go 1.22+、Node 18+。

```bash
# 后端（stdlib-only 依赖，离线可跑）
go build ./... && go test ./...

# 前端
cd frontend && npm install && npm run build

# 桌面打包（M2 壳层落地后可用）
wails build
```

> 离线说明：首次构建需联网拉取依赖并执行 `go mod vendor`；此后仓库内 `vendor/` 保证离线可构建。

## 提交白名单例外（必须入库，勿清理）

| 路径 | 理由 |
| --- | --- |
| `vendor/` | Go 官方 vendor 机制，离线/新环境可构建 |
| `frontend/wailsjs/` | Wails 生成绑定，前端编译依赖，避免构建顺序耦合 |

其余生成物、运行时数据、密钥一律不入库——完整规则见 `.gitignore` 与 `docs/STANDARDS.md`。

## 架构守卫

```bash
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/arch_check.ps1
```

提交前必须通过（R1 依赖方向 / R2 禁止吞错 / R3 工具超时契约 / R4 包注释）。

## 里程碑

| 里程碑 | 内容 | 状态 |
| --- | --- | --- |
| M0 | 脚手架 + 方案文档体系 + 守卫 | 进行中 |
| M1 | 事件账本会话内核（JSONL + 崩溃重放） | 未开始 |
| M2 | 流式对话环（provider 纪律 + ReAct + UI） | 未开始 |
| M3 | 文件编辑环 | 未开始 |
| M4 | 命令+git 环 | 未开始 |
| M5 | 打包与四环验收 | 未开始 |

> 历史版本（含旧实现与新架构分析）完整保存在 `legacy` 分支，可随时查阅。
