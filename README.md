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
│   └── interfaces.py        # SnapshotStore 协议契约
├── core/                    # 核心编排引擎层 (状态机与安全治理)
│   ├── engine.py            # LangGraph StateGraph 装配、热重载与预算调度
│   ├── permissions.py       # HITL 权限守卫 (ask / auto / read_only 模式)
│   ├── tool_dispatcher.py   # 只读并发 + 写串行调度器 & Doom-Loop 熔断
│   ├── syntax_guard.py      # 落盘前 JIT 静态预审 (Python / JSON / TOML / XML)
│   ├── compactor.py         # 智能上下文压缩与结构化摘要折叠
│   ├── doctor.py            # 环境体检 (/doctor) 与上下文占用剖析 (/context)
│   ├── search.py            # 跨平台沙箱 grep_search 与分页 read_file
│   ├── repo_map.py          # AST 代码拓扑全景生成 (含行号范围与安全剪枝)
│   ├── planner.py           # Planner-Worker 任务看板状态管理
│   ├── subagent.py          # 只读沙箱子智能体执行器
│   ├── usage_meter.py       # Token 计量器与阶梯缓存计费
│   ├── polyglot.py          # 多语言项目识别与验证命令探测
│   ├── history.py           # 内存级可逆快照与 Rollback
│   ├── git_store.py         # Git-Native 无损 Diff / 回滚 / 提交适配器
│   ├── session_store.py     # 会话、计划与快照磁盘持久化
│   └── diff.py / state.py / prompts.py
├── tools/                   # 基础设施与原子工具层
│   ├── file_ops.py          # 原子写入、单点替换/replace_all 编辑与可逆删除
│   ├── execution.py         # 跨平台子进程与危险命令防线沙箱
│   ├── plan_ops.py          # 任务计划看板工具
│   └── subagent_ops.py      # 子智能体调研委托工具
├── skills/                  # 扩展技能加载层 (.tiancode/skills/<name>/SKILL.md)
├── eval/                    # 本地自动化基准评测框架 (BenchmarkRunner)
└── cli/                     # 终端表现层
    ├── main.py              # CLI 参数解析与入口
    ├── repl.py              # 交互式 Shell 工作台
    ├── commands.py          # Slash 斜杠命令分发器
    └── formatter.py         # 流式输出、Diff 着色与交互渲染
```

---

## 🚀 核心特性

1. **双模启动与可控权限门禁 (HITL Permissions)**：
   - **默认交互审批 (`ask`)**：非只读工具（`write_file`、`edit_file`、`delete_file`、`rollback_turn`、`shell`、`run_python`）在执行前弹窗确认，支持 `[y] 仅本次 / [a] 本会话允许 / [n] 拒绝`（非交互无回调严格 Fail-Closed）。
   - **全自动模式 (`--yes` / `-y` / `/mode auto`)**：跳过确认，适合受信任仓库或自动化流；仍受底层危险命令沙箱与 `recursion_limit` 保护。
   - **只读审计模式 (`--read-only` / `/mode read_only`)**：禁止一切写操作与外部命令执行，仅允许只读源码检索与子智能体分析。
2. **Git-Native 无损快照与可逆编辑**：
   - `write_file` / `edit_file`（含唯一性防误替换与 `replace_all`）/ `delete_file` / `rollback_turn` 统一纳入快照追踪。
   - `/diff` 实时对比补丁，`/undo` 安全撤销本轮全部文件变更，`/commit` 自动生成 Conventional Commit。
3. **落盘前 AST / 格式即时守卫与闭环自愈**：
   - 写入前校验 Python AST、JSON、TOML（Python 3.10 自动兼容 `tomli`）与防 XXE XML；写后按语言/构建工具探测测试并触发自愈修复（可用 `--no-heal` 关闭）。
4. **混合并发调度与死循环 / 超时双重保险**：
   - `SafeToolDispatcher` 将只读检索线程池并发执行、修改操作严格串行，触发重复参数调用自动启用 Doom-Loop 熔断，同时为 LLM 配置重试与超时上限及状态机 `recursion_limit=50`。
5. **上下文智能压缩、环境体检与成本看板**：
   - 字符/消息双轨预算控制、`max_context_chars` 超限自动折叠，配套 `/compact`、`/context`、`/doctor` 与 `/cost`（`/usage`）。

---

## 📦 安装与配置

### 1. 安装开发模式
在项目根目录下执行（支持 Python `>= 3.10`）：
```bash
pip install -e ".[dev]"
```
安装后将注册 `tiancode` 与 `tc` 全局 CLI 命令。

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
- `/config show` 与 `/config set <prop> <val>`：查看或修改持久化配置并即时生效（支持 `key`、`url`、`model`、`max_chars`、`max_msgs`、`timeout`、`auto_heal`）
- `/doctor`：一键诊断配置、工作目录读写权限、Git 状态与 Python/pytest 环境
- `/context` & `/compact`：分析当前消息字符占用或立即执行上下文折叠压缩
- `/cost`（`/usage`）：查看推理轮次、缓存命中率与预估费用
- `/map` / `/plan` / `/skills`：查看 Repo Map 代码拓扑、Planner-Worker 看板及已加载技能
- `/sessions` / `/switch <id>` / `/clear` / `/status` / `/help`：会话列表、热切换、清空与状态查看
- `/diff` / `/undo` / `/commit [msg]` / `/eval [problem_id]`：代码补丁对比、无损回滚、提交与基准评测

---

## 📄 开源协议

本项目基于 [MIT License](LICENSE) 开源。
