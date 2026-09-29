# 湉码 / tiancode

**Windows 桌面 AI 编程工作台**——单 exe、本地数据、可插拔的模型与工具。
对话流式不丢字、文件命令受控执行、会话崩溃可恢复；模型渠道、MCP、技能都能在对话里热插拔。

> 诚实的现状说明：**模型与工具的"可装卸"已经落地**（渠道池、MCP、技能都是动态的）；
> **"记忆"与"编排"目前是规划项**——会话账本只做单会话恢复，暂无跨会话长期记忆，
> 也没有多 Agent 编排。详见文末 [Roadmap](#roadmap未完成)。

- 架构与分层：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
- 行为契约：[docs/CONTRACTS.md](docs/CONTRACTS.md)
- 工程规范：[docs/STANDARDS.md](docs/STANDARDS.md)
- 版本里程碑：[docs/MILESTONES.md](docs/MILESTONES.md)

## 功能总览

### 对话

- 流式回复（思考/正文分段）、消息队列（回合中提交自动排队）、Esc 中断
- **多会话并行**：回合进行中可切换/新建会话，流式事件各归各位
- 会话历史落本地账本（JSONL），**崩溃/重启后原样恢复**；按工作区分组、置顶、重命名、导出 Markdown
- 每会话独立草稿与输入历史；发送即有"正在思考"反馈，上游挂死有看门狗兜底（错误可见，绝不无声卡住）

### 模型渠道（可装卸）

- **多渠道池**：OpenAI 兼容 / Anthropic / ChatGPT 订阅（Codex）三种协议；多渠道按优先级/权重选路，故障自动切换
- **多凭证**：单渠道多 Key 轮询，坏 Key 单独禁用/恢复（auto_ban 可配）
- **渠道级鉴权方式**：Bearer / 自定义请求头 / URL 参数 / 无鉴权，兼容各种中转与自建网关
- **连通性测试**：行内一键测试（走真实链路含模型映射），显示延迟或失败原因
- **模型选择器**：输入框上方直接切换"渠道 × 模型"，所见即所跑
- **ChatGPT 订阅 OAuth**：浏览器授权绑定，凭证临期自动续期

### 工具与扩展（可装卸）

- 内置：文件读写/精准替换、命令执行（超时与后台日志）、git 只读、内容搜索
- **MCP**（stdio / HTTP）：对话里让 AI 自己安装，保存即热生效（连接懒拉起）
- **技能**：Markdown 正文按需注入，同样对话中热添加
- 管理面板打开期间实时刷新，**无需重启**
- 任务清单（todo）与向用户提问（ask_user）为一等交互

### 安全与受控

- **审批闸门**：按工具名配置需要确认的操作，拒绝原因回传给模型；审批通道故障默认拒绝
- **工作区受控**：文件/命令/搜索的根锁定在所选工作区；未选工作区 = 纯对话模式（本地工具下线，不越界）
- 密钥只存本机（`%APPDATA%\tiancode`），永不入库

### 全局代理

- 支持为所有上游请求（模型/授权）配置 http(s) 代理；配置无效会显式报错而非静默直连

## 快速开始

### 方式一：安装器（推荐）

从 `dist/` 运行 `tiancode-setup-vX.Y.Z.exe`：安装到 `%LOCALAPPDATA%\Programs\tiancode`，
创建桌面与开始菜单快捷方式与卸载项。支持 `-quiet`、`-dir <目录>`、`-no-desktop-shortcut`；
桌面路径按注册表解析（OneDrive 重定向也能正确落位）。卸载：系统"应用和功能"或 `tiancode-setup.exe -uninstall`。

首次启动：在**侧栏底部「渠道管理」**里添加渠道（BaseURL / API Key / 模型），保存即用；
需要海外上游时在同一面板配置**全局代理**（如 `http://127.0.0.1:7897`）。

### 方式二：从源码构建

前置：Go 1.22+、Node 20.19+（推荐 22）。

```bash
# 1) 前端 —— 必须排在 Go 编译之前。
#    为什么：main.go 用 //go:embed all:frontend/dist 把前端产物嵌进单 exe，
#    先编 Go 只会得到一个没有界面的空壳。
cd frontend && npm install && npm run build && cd ..

# 2) 后端：编译与测试（vendor 已入库，离线可跑）
go build ./... && go test ./...

# 3) 桌面应用
go build -o bin/tiancode.exe . && ./bin/tiancode.exe
```

> 离线说明：`vendor/` 已入库，`GOPROXY=off go build ./...` 可直接构建。
> 仓库里入库了占位文件 `frontend/dist/.gitkeep`，保证全新克隆也能通过 `go build ./...`；
> `npm run build` 会清空 `dist/`，构建后请执行 `git checkout -- frontend/dist/.gitkeep` 复原占位文件。

### 配置文件（可选）

配置以用户级文件为主，环境变量为覆盖（优先级：env > 文件）：`%APPDATA%\tiancode\config.json`。
渠道与密钥实际由**渠道管理**（`channels.json`）持有，配置文件仅作迁移来源。
环境变量：`TIANCODE_BASE_URL` / `TIANCODE_API_KEY` / `TIANCODE_MODEL` / `TIANCODE_WORKSPACE`；
自定义配置路径：`tiancode.exe -config <路径>`。

## 内置工具

| 工具 | 能力 | 关键契约 |
| --- | --- | --- |
| `fs` | 读写文件（原子写）、精准替换（多处匹配默认拒绝）、目录列举 | C-FS-1~7 |
| `shell` | 命令执行（默认 120s 超时、超时返回部分输出、后台任务日志有界） | C-TOOL-1~5 |
| `git` | 只读查看 status / diff / log | — |
| `search` | 工作区内容搜索（有界、跳过忽略目录、可配超时） | C-SEARCH-1~6 |
| `skill` | 按名读取技能正文（清单在系统说明里） | — |
| `mcp` | 转发调用已启用的 MCP 服务器工具（连接懒拉起） | — |
| `ext_manage` | 扩展自管理：模型可在对话中安装/删除 MCP 与技能 | 保存后热生效 |
| `todo` / `ask_user` | 任务清单与向用户提问（交互式审批之外的轻量确认） | — |

## Roadmap（未完成）

按"热插拔插件化工作台"的愿景逐项对账——**做完的在上面的功能总览里，没做完的在这里**：

| 项 | 现状 | 说明 |
| --- | --- | --- |
| **记忆系统** | 未实现 | 目前只有单会话账本恢复；跨会话长期记忆、项目级上下文、记忆的装卸位都是空白 |
| **编排（多 Agent）** | 未实现 | 只有单 Agent ReAct 循环；子代理、工作流、任务编排均未开始 |
| **统一 Rail 抽象** | 未实现 | 模型/工具/扩展目前是三套各自独立的机制，没有统一的插件框架与生命周期管理 |
| **Claude 订阅 OAuth** | 未实现 | ChatGPT 订阅授权已上线；Claude 订阅授权（PKCE + code 粘贴回调）在计划中 |
| **更多协议适配器** | 未实现 | 已有 OpenAI 兼容 / Anthropic / Codex；Gemini、Bedrock 等按需新增 |
| **自动更新** | 未实现 | 升级目前靠重新运行安装器 |

## 开发与发布

```powershell
# 发布（六道门禁 + 安装器 + 便携包，版本号取自根目录 VERSION）
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release.ps1

# 安装/卸载端到端冒烟（安装 → 启动 → 卸载 → 注册项校验）
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/install-smoke.ps1

# 架构守卫（提交前必须通过：R1 依赖方向 / R2 禁止吞错 / R3 工具超时 / R4 包注释）
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/arch_check.ps1

# 真实上游冒烟（可选）
TIANCODE_SMOKE_BASEURL=... TIANCODE_SMOKE_APIKEY=... TIANCODE_SMOKE_MODEL=... \
  go test ./internal/platform/openaiprovider/ -run TestSmoke_RealUpstream -v
```

## 提交白名单例外（必须入库，勿清理）

| 路径 | 理由 |
| --- | --- |
| `vendor/` | Go 官方 vendor 机制，离线/新环境可构建 |
| `frontend/wailsjs/` | Wails 生成绑定，前端编译依赖，避免构建顺序耦合 |
| `build/windows/icon.ico` | 应用图标源资源（`tools/genicon.ps1` 的输出），syso 由它生成 |

其余生成物、运行时数据、密钥一律不入库——完整规则见 `.gitignore` 与 `docs/STANDARDS.md`。
