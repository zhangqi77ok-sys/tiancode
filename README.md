# ⚡ TianCode

> **极简、高可用、可控状态机的终端 AI 编程智能体 (Coding Agent CLI)**
> 
> 基于 **LangGraph 状态机** + **Clean Architecture (整洁架构)** 设计，为开发者提供类 Claude Code / Cursor CLI 的纯终端自主编程体验。

---

## 🏛️ 核心架构设计 (Clean Architecture)

TianCode 严格遵循**整洁架构 / 六边形架构 (Ports and Adapters)** 原则进行分层解耦：

```text
tiancode/
├── config/             # 配置适配层 (分层加载: CLI参数 > 环境变量 > 默认配置)
│   └── settings.py
├── domain/             # 领域实体与抽象契约层 (零外部框架依赖)
│   ├── models.py       # FileDiff, ExecutionResult, FileSnapshot 实体
│   └── interfaces.py   # SnapshotStore 协议契约
├── core/               # 核心编排引擎层 (状态机驱动)
│   ├── engine.py       # LangGraph StateGraph 装配与调度器
│   ├── history.py      # 时光倒流与统一 Diff 计算引擎
│   ├── prompts.py      # 自愈提示词与工程规约
│   └── state.py        # 消息裁剪与上下文瘦身
├── tools/              # 基础设施与原子工具层 (适配器)
│   ├── file_ops.py     # 局部手术级读写改与防御
│   └── execution.py    # 跨平台子进程安全沙箱
└── cli/                # 终端表现层 (用户交互)
    ├── main.py         # CLI 统一入口 (argparse)
    ├── repl.py         # 交互式 Shell 工作台
    ├── commands.py     # Slash 命令分发器 (/undo, /diff, /clear)
    └── formatter.py    # ANSI 彩色渲染与 Diff 补丁展示
```

---

## 🚀 核心特性

1. **双模启动**：
   - **单发模式 (One-Shot)**：`tiancode "修复 main.py 的除零异常"`，自动改代码、测代码、汇报后退出。
   - **交互终端 (REPL)**：`tiancode` 进入持续对话工作台，保持多轮上下文。
2. **Git-Native 智能协同 (时光倒流与自动微提交)**：
   - `/diff`：实时查看当前会话修改的所有文件的 Git 风格红绿 Diff 补丁。
   - `/undo`：安全无损快照回滚，精准撤销智能体在本轮修改与新建的文件，不破坏用户自身未暂存的修改或 Git 暂存区。
   - `/commit`：AI 自动根据代码变动生成符合 Conventional Commits 规范的提交信息并自动执行 `git commit`。
   - `/eval`：本地微型基准评测机 (Micro Benchmark)，支持算法与工程挑战题的自主求解与沙箱验证。
3. **真实子进程自愈闭环**：
   - 绝不“只写不测”，改完代码自动调用系统 Python 真实运行，捕获 Traceback 自动自我修复。
4. **上下文智能压缩与 Token 预算熔断 (Smart Context Compaction)**：
   - 动态字符与消息双轨预算控制，防止长会话或海量工具输出导致上下文窗口爆炸。
   - 自动生成结构化【上下文智能折叠摘要】卡片保留历史关键事实与文件变更，无缝桥接前后文。
   - 提供 `/compact` 斜杠指令，支持在交互终端中手动触发压缩并查看节省字符统计。
5. **落盘前 AST 语法即时守卫与极速自愈 (Pre-Write JIT AST Syntax Guard)**：
   - 在 `write_file` / `edit_file` 文件真正落盘前，执行毫秒级内存静态语法预审 (支持 Python `ast.parse` 与 JSON 格式校验)。
   - 发现语法错误立即在落盘前阻断，杜绝污染工作区代码，并输出包含行号、列号与代码指针切片的诊断卡片，驱动大模型极速自愈修复。
6. **会话 Token 计量器与费用实时看板 (Token Usage & Cost Meter)**：
   - 精确追踪每轮推理的冷启动输入、Prompt 缓存命中与输出生成 Tokens，并内置多模型阶梯费率计算。
   - 提供 `/cost`（或 `/usage`）斜杠指令查看缓存命中率、节省金额与分项账单，且会话消耗随磁盘持久化同步还原。
7. **跨平台沙箱检索与混合并发熔断调度器 (`grep_search` & `SafeToolDispatcher`)**：
   - 内置纯 Python 跨平台 `grep_search` 与行号分页 `read_file`，支持目录剪枝、二进制过滤及软链接物理防逃逸。
   - 混合调度引擎使多条只读检索工具在线程池中并发加速，写入与执行工具严格串行保障快照一致，并对连续重复调用自动触发 Doom-Loop 死循环熔断。

---

## 📦 安装与配置

### 1. 安装开发模式
在项目根目录下执行：
```bash
pip install -e .
```
安装后，系统将注册 `tiancode` 与 `tc` 两个全局命令。

### 2. 配置 API Key
TianCode 支持任何兼容 OpenAI 协议的模型（推荐 DeepSeek）：
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

---

## 💡 使用示例

### 单发执行
```bash
tc "在当前目录下创建一个 utils.py，写一个斐波那契数列函数并用 assert 自测"
```

### 进入交互式工作台
```bash
tc
```
进入后可使用以下指令：
- `tiancode > 帮我给 utils.py 加上阶乘函数并测试`
- `tiancode > /diff` (查看代码变动)
- `tiancode > /undo` (后悔了？一键撤回修改)
- `tiancode > /compact` (智能折叠长会话历史，释放 Token 预算)
- `tiancode > /cost` (查看当前会话 Token 消耗、缓存命中率与预估费用)
- `tiancode > /sessions` (查看历史持久化会话)
- `tiancode > /switch <session_id>` (恢复历史会话)
- `tiancode > /clear` (清空对话记忆)
- `tiancode > exit` (退出)
