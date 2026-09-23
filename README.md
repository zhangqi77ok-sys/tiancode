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
# 后端（vendor 已入库，离线可跑）
go build ./... && go test ./...

# 前端
cd frontend && npm install && npm run build

# 桌面应用（exe 直接可跑；wails CLI 可选）
go build -o bin/tiancode.exe . && ./bin/tiancode.exe
```

> 离线说明：`vendor/` 已入库，`GOPROXY=off go build ./...` 可直接构建（已验证）。

## 运行配置（环境变量）

密钥只经环境变量注入，**永不入库**（docs/STANDARDS.md §1）：

| 变量 | 说明 |
| --- | --- |
| `TIANCODE_BASE_URL` | OpenAI 兼容网关根地址（含 `/v1`） |
| `TIANCODE_API_KEY` | 供应商密钥 |
| `TIANCODE_MODEL` | 默认模型（如 `grok-4.6`） |
| `TIANCODE_WORKSPACE` | 工作区目录（fs/shell/git 工具的受控范围，缺省当前目录） |

```powershell
$env:TIANCODE_BASE_URL='https://your-gateway/v1'; $env:TIANCODE_API_KEY='sk-...'; $env:TIANCODE_MODEL='your-model'
.\bin\tiancode.exe
```

## 内置工具

| 工具 | 能力 | 关键契约 |
| --- | --- | --- |
| `fs` | 读写文件（原子写）、精准替换（多处匹配默认拒绝） | C-FS-1~4 |
| `shell` | 命令执行（默认 120s 超时、超时返回部分输出、后台任务日志有界） | C-TOOL-1~5 |
| `git` | 只读查看 status / diff / log | — |

## 真实上游冒烟测试（可选）

```bash
TIANCODE_SMOKE_BASEURL=... TIANCODE_SMOKE_APIKEY=... TIANCODE_SMOKE_MODEL=... \
  go test ./internal/platform/openaiprovider/ -run TestSmoke_RealUpstream -v
```

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
