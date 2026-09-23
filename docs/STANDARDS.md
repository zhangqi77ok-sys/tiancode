# 工程规范（STANDARDS）

> 本文是全部工程规范的**单一定义源**。违反本文的代码不允许合入 main。
> 守卫由 `scripts/arch_check.ps1` 与 CI 门禁强制执行；规则演进必须先改 ADR（见 `adr/0004-guard-rules.md`）。

## 1. Git 提交纪律

### 永不入库（.gitignore 强制）

| 类别 | 路径 |
| --- | --- |
| 依赖与构建产物 | `node_modules/`、`frontend/dist/`、`*.exe`、`bin/`、`build/`、`dist/` |
| 运行时数据 | `data/`、`*.db`、`*.sqlite`、`logs/`、`*.log` |
| 密钥与环境 | `.env`、`.env.*`、`*.local`（API Key 只进环境变量） |
| 临时与个人 IDE | `tmp/`、`.DS_Store`、`Thumbs.db`、`.idea/`、`.vscode/` |
| Go 工具产物 | `coverage.out`、`*.test`、`*.out` |

### 必须入库的例外（白名单，防误删）

| 路径 | 理由 |
| --- | --- |
| `vendor/` 及其**内部全部文件**（含 `*.exe` 等二进制） | Go 官方 vendor 机制保证离线/新环境可构建。**vendor 内的文件一个都不能少**：wails 的 `webview2installer.go` 用 `//go:embed MicrosoftEdgeWebview2Setup.exe` 无条件嵌入该二进制，缺了它 `go build ./...` / `go vet` / `go test` 三条全红 |
| `frontend/dist/.gitkeep` | 占位文件：`main.go` 有 `//go:embed all:frontend/dist`，**要求该目录存在**，否则全新克隆无法编译 |
| `cmd/installer/payload/.gitkeep` | 同上：安装器载荷目录需存在（载荷本身由 `release.ps1` 生成，不入库） |

**两条容易踩的坑（都曾在本仓库真实发生）**：

1. **取反规则必须写在 `.gitignore` 最后，且父目录不能被排除。**
   gitignore 是"后匹配者优先"，但 gitignore(5) 明确规定：**父目录被排除时，无法再取反其中的文件**。
   本仓库曾用 `dist/`（未锚定）连带排除了 `frontend/dist` 目录本身，使 `!frontend/dist/.gitkeep`
   变成一句废话——占位文件因此从未入库，直接导致新克隆构建失败。现该三条构建产物目录均锚定为
   `/bin/` `/build/` `/dist/`。
2. **白名单必须写成规则，不能只在注释里列清单。**
   本仓库曾把白名单写成注释，于是 `*.exe` 把 vendor 内的 wails 安装器二进制静默挡在仓库外。

### 提交流程

1. **禁止盲提交**：`git add` 前必须 `git status` 审计；改动含密钥/绝对路径/临时文件一律不提交。
2. **Conventional Commits**：`feat|fix|docs|refactor|test|chore(scope): 描述`。
3. 每次提交 = 可构建、可测试的原子单元；提交前 `go build ./... && go test ./... && scripts/arch_check.ps1` 三绿。

## 2. 包分层规范

| 层 | 位置 | 允许依赖 | 禁止 |
| --- | --- | --- | --- |
| 壳 | `main.go`、`app/` | `internal/app`、wails | 业务规则 |
| 编排 | `internal/app` | `internal/core/*` | 直接执行 IO（文件/进程/网络） |
| 内核 | `internal/core/*` | 仅 stdlib + core 内端口 | import 适配器/壳/编排（守卫 R1） |
| 适配器 | `internal/platform` | core 端口 + stdlib + 单一外部依赖 | import 编排/壳 |

命名与组织：

- 包名小写单词（`agent`/`session`/`llm`/`tools`/`platform`）；接口统一 `Port` 后缀。
- 每个非平凡包必须有包级 doc 注释，回答三问：**做什么 / 被谁依赖 / 依赖谁**（守卫 R4）。
- 单文件 >400 行必须拆分；相同 helper 出现第 2 次即评估提升到 `internal/platform`。

## 3. 注释规范

- 所有**导出标识符**（类型/函数/常量/导出字段）必须有 godoc 注释——CI 门禁（golangci-lint revive：`package-comments` + `exported`）。
- 注释写**为什么**：魔数、超时值、Windows 特判、并发约束必须注明缘由。
- 借鉴的外部实现必须注明参照来源（如"流式纪律参照 new-api relay/helper/stream_scanner.go"）。
- 前端：composable/store 导出函数必须注释；组件 props 全部显式 TS 类型。

## 4. 文档规范

- 文档与代码**同一 PR** 更新，不允许文档滞后：
  - 新模块落地 → 同步更新 `ARCHITECTURE.md`（分层图 + 目录）与 `CONTRACTS.md`（契约条目）；
  - 重大技术决策 → 先写 ADR 再写代码；
  - 新行为契约 → 同步登记 `TESTING.md` 契约测试清单。
- 文档目录：`ARCHITECTURE.md` / `CONTRACTS.md` / `TESTING.md` / `MILESTONES.md` / `STANDARDS.md` / `adr/`。
- `README.md` 承诺：新环境 5 分钟跑通构建/测试/运行三条命令。

## 5. 交付纪律（每次开发完成必须打包）

**每次开发完成（每个里程碑/每次功能提交）必须执行发布流水线产出安装包**，不允许只留源码：

```bash
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release.ps1
```

流水线强制顺序：门禁（gofmt/vet/test/arch_check）→ 前端构建 → 应用 exe → **安装包** → 便携包。
产物（`dist/`，gitignore）：

| 产物 | 说明 |
| --- | --- |
| `tiancode-setup-v<版本>.exe` | **原生 Go 安装器**（无需 NSIS/Inno，离线可构建）：装到 `%LOCALAPPDATA%\Programs\tiancode`，创建开始菜单快捷方式与"应用和功能"卸载项；`-quiet` 静默模式供自动化 |
| `tiancode-v<版本>-portable.zip` | 便携包（exe + README） |

版本号唯一来源：仓库根 `VERSION`。安装器载荷经 `installer_payload` 构建标签内嵌，
常规 `go build ./...` 不受影响。

## 6. CI 门禁

落地位置：**`.github/workflows/ci.yml`**（`windows-latest`）。该工作流的步骤顺序不可调换——
必须在 Go 门禁之前构建前端，因为 `main.go` 用 `//go:embed all:frontend/dist` 内嵌前端产物。

| 门禁 | 命令 | 通过标准 |
| --- | --- | --- |
| 格式 | `gofmt -l main.go app internal cmd` | 空输出（vendor 为第三方代码，不纳入格式管治） |
| 静态检查 | `go vet ./...` | 零错误 |
| 注释/风格 | `golangci-lint run` | 零告警 |
| 架构守卫 | `scripts/arch_check.ps1` | 退出码 0 |
| 测试 | `go test ./...` | 全绿 |

**行尾前提**：格式门禁要求工作区为 LF，依据是仓库根的 `.gitattributes`（`*.go text eol=lf`）。
不要把这件事交给各人的 `core.autocrlf`——Git for Windows 默认 `true`，会把工作区签出为 CRLF，
而 gofmt 只输出 LF，于是 `gofmt -l` 列出全部 Go 文件、格式门禁**在任何 Windows 机器上都永远无法通过**。
行尾是仓库约定，必须随仓库分发。
