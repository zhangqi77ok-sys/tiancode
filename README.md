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
# 1) 前端 —— 必须排在 Go 编译之前。
#    为什么：main.go 用 //go:embed all:frontend/dist 把前端产物嵌进单 exe，
#    先编 Go 只会得到一个没有界面的空壳。
cd frontend && npm install && npm run build && cd ..

# 2) 后端：编译与测试（vendor 已入库，离线可跑）
go build ./... && go test ./...

# 3) 桌面应用（exe 直接可跑；wails CLI 可选）
go build -o bin/tiancode.exe . && ./bin/tiancode.exe
```

> 离线说明：`vendor/` 已入库，`GOPROXY=off go build ./...` 可直接构建（已验证）。
> **为什么第 2 步不必先构建前端**：仓库里入库了占位文件 `frontend/dist/.gitkeep`，
> 它保证全新克隆（尚无 `frontend/dist` 目录）也能通过 `go build ./...` / `go vet ./...` / `go test ./...`；
> 否则 `go:embed` 会因"目录不存在"直接报错（`pattern all:frontend/dist: no matching files found`）。
> `npm run build` 会清空 `dist/`，构建后请执行 `git checkout -- frontend/dist/.gitkeep` 复原占位文件。

## 配置（安装版首次运行）

配置以**用户级文件为主**，环境变量为覆盖（优先级：env > 文件），密钥永不入库。

配置文件路径：`%APPDATA%\tiancode\config.json`

```json
{
  "baseUrl": "https://your-gateway/v1",
  "apiKey": "sk-...",
  "model": "grok-4.6",
  "workspace": "C:\\path\\to\\your\\project"
}
```

- **首次运行**：文件不存在时自动生成该模板并弹窗提示路径与必填字段（不会静默退出）；
- 会话数据固定在 `%APPDATA%\tiancode\sessions`（与安装目录解耦，卸载不删会话）；
- 启动失败会弹错误框并写入 `%APPDATA%\tiancode\tiancode.log`；
- 开发/脚本部署可用环境变量覆盖：`TIANCODE_BASE_URL` / `TIANCODE_API_KEY` / `TIANCODE_MODEL` / `TIANCODE_WORKSPACE`；
- 自定义配置路径：`tiancode.exe -config <路径>`。

```powershell
# 开发模式（env 覆盖，无需配置文件）
$env:TIANCODE_BASE_URL='https://ss2a.top/v1'; $env:TIANCODE_API_KEY='sk-...'; $env:TIANCODE_MODEL='grok-4.6'; $env:TIANCODE_WORKSPACE='d:\your\project'
.\bin\tiancode.exe
```

## 内置工具

| 工具 | 能力 | 关键契约 |
| --- | --- | --- |
| `fs` | 读写文件（原子写）、精准替换（多处匹配默认拒绝） | C-FS-1~4 |
| `shell` | 命令执行（默认 120s 超时、超时返回部分输出、后台任务日志有界） | C-TOOL-1~5 |
| `git` | 只读查看 status / diff / log | — |

## 发布与安装（每次开发完成必做）

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release.ps1
```

| 产物（`dist/`） | 说明 |
| --- | --- |
| `tiancode-setup-v0.1.0.exe` | 原生安装器（无需 NSIS）：安装到 `%LOCALAPPDATA%\Programs\tiancode`，创建开始菜单快捷方式与卸载项 |
| `tiancode-v0.1.0-portable.zip` | 便携包（exe + README） |

安装器支持 `-quiet`（静默，供脚本部署）与 `-dir <目录>`（自定义安装位置）；
卸载：开始菜单 →“应用和功能”，或运行安装目录下 `tiancode-setup.exe -uninstall`。
版本号来源：根目录 `VERSION`。

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
