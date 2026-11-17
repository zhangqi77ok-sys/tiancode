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
   - `/undo`：一键执行 `git restore . && git clean -fd`，瞬间恢复到修改前。
   - `/commit`：AI 自动根据代码变动生成符合 Conventional Commits 规范的提交信息并自动执行 `git commit`。
3. **真实子进程自愈闭环**：
   - 绝不“只写不测”，改完代码自动调用系统 Python 真实运行，捕获 Traceback 自动自我修复。
4. **上下文自动瘦身**：
   - 动态裁剪陈旧的中间报错，保留原始目标与最近操作，避免 Token 爆炸。

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
- `tiancode > /clear` (清空对话记忆)
- `tiancode > exit` (退出)
