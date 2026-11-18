# ⚡ TianCode

> **极简、高可用、可控状态机的终端 AI 编程智能体 (Coding Agent CLI)**
>
> 基于 **LangGraph 状态机** + **Clean Architecture (整洁架构)** 设计，内置人机协同权限门禁 (HITL)、跨平台命令与语法安全守卫、智能上下文折叠与 Token 成本计量，为开发者提供可控可回滚的纯终端编程体验。

---

## 🏛️ 核心架构设计 (Clean Architecture)

TianCode 严格遵循**整洁架构 / 六边形架构 (Ports and Adapters)** 原则进行分层解耦：

```text
tiancode/
├── config/                  # 配置适配层 (CLI 参数 > 环境变量 > ~/.tiancode/config.json)
│   └── settings.py
├── domain/                  # 领域实体与抽象契约层 (零外部框架依赖)
│   ├── models.py            # FileDiff, ExecutionResult, FileSnapshot 等实体
│   ├── memory.py            # 三层认知记忆实体 (Workspace / Episode / Semantic)
│   └── interfaces.py        # SnapshotStore 协议契约
├── core/                    # 核心编排引擎层 (状态机与安全治理)
│   ├── engine.py            # LangGraph StateGraph 装配、记忆唤醒与动态切目录
│   ├── stream_events.py     # 流式事件转换与会话状态快照恢复助手
│   ├── project_rules.py     # AGENTS.md / TIANCODE.md 分层加载与 /init 生成器
│   ├── context_mentions.py  # @file 模糊路径匹配与安全沙箱源码片段直注
│   ├── mcp_transports.py    # 同步 MCP stdio 子进程与 Streamable HTTP/SSE 传输层
│   ├── mcp_client.py        # 全局/工作区 mcp.json 分层解析与 MCP 服务生命周期管理
│   ├── llm_adapter.py       # 推理模型 reasoning_content 与 <think> 流式适配器
│   ├── cognitive_memory.py  # 跨会话三层大脑记忆仓库、时间衰减与联想召回
│   ├── permissions.py       # HITL 权限守卫 (ask / auto / read_only + 人工纠正)
│   ├── tool_dispatcher.py   # 只读并发 + 写串行调度器 & Doom-Loop 熔断
│   ├── syntax_guard.py      # 落盘前 JIT 静态预审 (Python / JSON / TOML / XML)
│   ├── compactor.py         # 智能上下文压缩与结构化摘要折叠
│   ├── doctor.py            # 环境体检 (/doctor) 与上下文占用剖析 (/context)
│   ├── search.py            # 跨平台沙箱 grep_search 与分页 read_file
│   ├── repo_map.py          # Python AST 与 TypeScript/JS 多语言代码拓扑全景生成
│   ├── planner.py           # Planner-Worker 任务看板状态管理
│   ├── subagent.py          # 只读沙箱子智能体执行器
│   ├── usage_meter.py       # Token 计量器与阶梯缓存计费
│   ├── polyglot.py          # 多语言项目识别与验证命令探测
│   ├── history.py           # 内存级可逆快照与 Rollback
│   ├── git_store.py         # Git-Native 无损 Diff / 回滚 / 提交适配器
│   ├── session_store.py     # 会话、计划、历史搜索与快照磁盘持久化
│   └── diff.py / state.py / prompts.py
├── tools/                   # 基础设施与原子工具层
│   ├── file_ops.py          # 原子写入、单点替换/replace_all 编辑与可逆删除
│   ├── execution.py         # 跨平台子进程与危险命令防线沙箱
│   ├── memory_ops.py        # 跨会话联想回忆 recall_memory、固化事实与切工作区
│   ├── mcp_ops.py           # 外部 MCP 工具 StructuredTool 适配与工作区热重载生命周期
│   ├── web_ops.py           # SSRF 防护的只读公网文档抓取 web_fetch 工具
│   ├── plan_ops.py          # 任务计划看板工具
│   └── subagent_ops.py      # 子智能体调研委托工具
├── skills/                  # 扩展技能加载层 (.tiancode/skills/<name>/SKILL.md)
├── eval/                    # 本地自动化基准评测框架 (BenchmarkRunner)
└── cli/                     # 终端表现层
    ├── main.py              # CLI 参数解析与入口
    ├── theme.py             # Dark Mode 色板令牌与 CleanMarkdown 组件库
    ├── widgets.py           # / & @file 复合补全、鼠标审批胶囊栏与 Live Spinner
    ├── repl.py              # 交互式工作台 (点击状态栏、@file 展开与启动提示)
    ├── command_ops.py       # 配置、费用、记忆、工作区与规约卡片渲染实现
    ├── commands.py          # Slash 斜杠命令分发路由
    └── formatter.py         # 思考折叠面板与 Rich 流式渲染引擎
```

---

## 🚀 核心特性

1. **双模启动、鼠标审批交互与人工实时纠正 (HITL Permissions & Steering)**：
   - **默认交互审批 (`ask`)**：非只读工具执行前弹窗提示，支持**鼠标左键直接点击胶囊按钮**或快捷键切换（`[y] 允许本次 / [a] 本会话允许 / [e] 人工纠正 / [n] 拒绝`），点击或输入自然语言纠正指令可立即指导 Agent 改道，底部状态栏支持鼠标点击/`Shift+Tab` 切换权限与自愈开关。
   - **全自动模式 (`--yes` / `-y` / `/mode auto`)**：跳过确认，适合受信任仓库或自动化流；仍受底层危险命令沙箱与 `recursion_limit` 保护。
   - **只读审计模式 (`--read-only` / `/mode read_only`)**：禁止一切写操作与外部命令执行，仅允许只读源码与历史记忆检索及子智能体分析。
2. **类人脑三层认知记忆与跨项目联想 (`CognitiveMemory`)**：
   - REPL 默认分配独立 `repl_*` 会话 ID 永不互相覆盖；每次对话存盘零额外 Token 自动巩固工程档案与情景简报。
   - 新会话启动默认注入轻量级近期工程索引（Subconscious Priming），聊起“刚刚那个项目”或其它工程时通过 `recall_memory` 跨库联想，需要读取代码时经 HITL 确认后 `switch_workspace` 安全切往目标沙箱目录。
3. **推理思考链可视化 & Dark Mode Rich 流式渲染**：
   - `ReasonerChatOpenAI` 兼容 DeepSeek-R1 `reasoning_content` 与 `<think>` 流（多轮工具调用自动回传且剥离历史旧思考防 400 报错）；思考过程展示 6 行滚动圆角微卡并随正文/工具调用自动折叠为单行摘要，正文 Markdown 与代码块使用 Monokai 卡片渲染。
4. **Git-Native 无损快照、落盘前语法守卫与自愈闭环**：
   - 统一纳入 `write_file` / `edit_file` / `delete_file` / `rollback_turn` 快照追踪；支持 Python AST、JSON、TOML、防 XXE XML 预审与多语言探测测试自愈（可用 `--no-heal` 关闭）。
5. **混合并发调度、上下文智能压缩与成本看板**：
   - `SafeToolDispatcher` 并发只读、串行修改并启用 Doom-Loop 死循环熔断；双轨预算超限自动折叠，配套 `/compact`、`/context`、`/doctor` 与 `/cost`（`/usage`）。
6. **`@file` 模糊引用与项目规约自动加载 (`AGENTS.md` / `TIANCODE.md`)**：
   - 输入 `@` 实时模糊弹出当前工作区文件列表，支持 `@path#L10-50` 精确行号直注并拦截越界与 `.env`/私钥等敏感文件；
   - 自动按全局 → 工作区顺序挂载 `AGENTS.md`/`TIANCODE.md`，支持 `/init [--force]` 扫描技术栈生成专属规范与 `/rules` 查看。
7. **标准 MCP (Model Context Protocol) 客户端集成**：
   - 分层加载 `~/.tiancode/mcp.json` 与工作区 `.tiancode/mcp.json`，同时支持 `stdio`（自动剥离敏感 LLM API Key 环境变量）与 Streamable `HTTP/SSE`；
   - 将外部工具标准化挂载为 `mcp__<server>__<tool>` `StructuredTool`，统一受 HITL `PermissionGuard` 门禁保护，切工作区自动回收旧子进程，配套 `/mcp [reload|tools]` 管理。

---

## 📦 安装与配置

### 1. 安装开发模式
在项目根目录下执行（支持 Python `>= 3.10`）：
```bash
pip install -e ".[dev]"
```
安装后将注册 `tiancode` 与 `tc` 全局 CLI 命令（亦支持 `python -m tiancode` 启动）。

### 2. 配置 API Key
支持任意 OpenAI 协议兼容模型（默认适配 DeepSeek）：
```bash
# Windows PowerShell
$env:TIANCODE_API_KEY = "你的_API_KEY"
$env:TIANCODE_BASE_URL = "https://api.deepseek.com"
$env:TIANCODE_MODEL = "deepseek-chat"

# Linux / macOS
export TIANCODE_API_KEY="你的_API_KEY"
export TIANCODE_BASE_URL="https://api.deepseek.com"
export TIANCODE_MODEL="deepseek-chat"
```
首次无环境变量启动时将自动引导写入用户配置，并在运行期间侦测外部配置变化实现热重载。

---

## 💡 CLI 参数与斜杠指令

### 常用命令行参数
- `tc "任务描述"`：单发执行（One-Shot），默认隔离生成 `oneshot_*` 会话；可配合 `--yes` / `-y` 自动授权，或 `--read-only` 进行纯只读审查。
- `tc --continue` (`-c`) / `tc --resume <session_id>`：断点恢复最近一次或指定历史会话。
- `tc --no-heal`：禁用写后自动测试与自愈循环。
- `tc --max-chars 60000`：指定上下文字符预算阈值（超出自动压缩折叠）。
- `tc --dir <path>` / `--model <name>` / `--key <key>` / `--base-url <url>` / `--version`。

### REPL 交互式斜杠指令
- `/mode [ask|auto|read_only]`：查看或动态切换当前运行时安全权限模式
- `/init [--force]` 与 `/rules`：自动探测技术栈/模块生成 `TIANCODE.md`，或查看当前已挂载规约清单
- `/mcp [reload|tools]`：查看外部 MCP 服务状态、已挂载工具明细，或在修改 `mcp.json` 后重连服务
- `/cd <目录>`：手动切换当前会话工作区沙箱并重锚 Git/文件工具
- `/memory [clear]` 与 `/remember [project:]<事实>`：查看、清空跨会话认知记忆档案或固化长期知识
- `/config show` 与 `/config set <prop> <val>`：查看或修改持久化配置并即时生效（支持 `key`、`url`、`model`、`max_chars`、`max_msgs`、`timeout`、`auto_heal`）
- `/doctor`：一键诊断配置、工作目录读写权限、Git 状态与 Python/pytest 环境
- `/context` & `/compact`：分析当前消息字符占用或立即执行上下文折叠压缩
- `/cost`（`/usage`）：查看推理轮次、缓存命中率与预估费用
- `/map` / `/plan` / `/skills`：查看 Repo Map 代码拓扑、Planner-Worker 看板及已加载技能
- `/sessions` / `/switch <id>` / `/clear` / `/status` / `/help` / `/exit`：会话列表、热切换、清空、状态与退出
- `/diff` / `/undo` / `/commit [msg]` / `/eval [problem_id]`：代码补丁对比、无损回滚、提交与基准评测

---

## 📄 开源协议

本项目基于 [MIT License](LICENSE) 开源。
