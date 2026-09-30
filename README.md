# 湉码 / tiancode

**Windows 桌面 AI 编程工作台**——单 exe、本地数据，模型渠道 / MCP / 技能均可在对话中热插拔。

架构与分层、行为契约、工程规范、版本里程碑见 [docs/](docs/) 下对应文档。

## 功能

- **对话**：流式回复、多会话并行、本地账本（JSONL）崩溃恢复、消息队列、Esc 中断
- **模型渠道**：OpenAI 兼容 / Anthropic / ChatGPT 订阅；多渠道选路故障切换、多 Key 轮询、连通性测试、OAuth 自动续期
- **工具与扩展**：内置 fs / shell / git / search；MCP 与技能在对话中热安装，保存即生效，无需重启
- **安全**：按工具配置审批闸门、文件命令锁定在工作区内（未选工作区 = 纯对话）、密钥只存本机
- **代理**：全局 http(s) 代理，配置无效显式报错而非静默直连

规划中（尚未实现）：记忆系统、多 Agent 编排、Claude 订阅 OAuth、Gemini/Bedrock 适配、自动更新。

## 快速开始

从 `dist/` 运行 `tiancode-setup-vX.Y.Z.exe` 安装。首次启动在侧栏底部「渠道管理」添加渠道（BaseURL / API Key / 模型），需要海外上游时在同一面板配置全局代理。

从源码构建（Go 1.22+、Node 20.19+）：

```bash
cd frontend && npm install && npm run build && cd ..   # 前端必须先于 Go 编译（go:embed）
go build ./... && go test ./...                        # vendor 已入库，离线可跑
go build -o bin/tiancode.exe . && ./bin/tiancode.exe
```

> `npm run build` 会清空 `frontend/dist/`，构建后执行 `git checkout -- frontend/dist/.gitkeep` 复原占位文件。

## 开发与发布

```powershell
# 发布（门禁 + 安装器 + 便携包，版本号取自 VERSION）
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release.ps1

# 安装/卸载冒烟
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/install-smoke.ps1

# 架构守卫（提交前必须通过）
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/arch_check.ps1
```

## 提交白名单例外（必须入库，勿清理）

`vendor/`（离线构建）、`frontend/wailsjs/`（Wails 生成绑定）、`build/windows/icon.ico`（图标源资源）。
