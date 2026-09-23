# 47 - 终结固定轮次硬限制、确立 AI 自主判断任务结束与防爆安全兜底 (Knowledge Base #47)

## ① 知识点与问题背景 (Context & Problem Statement)

在主流 Coding Agent（如 Cursor Agent、Windsurf、Claude Code 等）的实际应用中，开发者经常面对多步联动的复杂任务（例如：全局检索 5 个文件、修改 3 处定义、运行编译器、发现测试报错、修复测试断言、再次运行验证）。
在此前 湉码 的实现中：
1. **人为生硬设置 24 轮（或 15 轮）硬上限**：一旦工具调用累计达到 24 轮，即便 AI 正在定位最后一个 Bug，微内核也会强行触发 `hit_cap`，输出“本轮工具自主调用已达上限，请发送继续”，强迫开发者介入交互打断上下文；
2. **缺乏模型自主决策完成的显式契约**：系统没有在提示词中向大模型交代“何时结束由你说了算”，导致模型在长任务中瞻前顾后，无法准确把控交付时机。

---

## ② 核心原理与知识内容 (Knowledge Content & Root Cause)

### 1. ReAct 单循环的真正终态：0 工具调用即自主交付
在标准 ReAct（Reasoning + Acting）单执行回路中：
- 当模型认为当前阶段还需要获取信息或改变状态时，它在响应中声明 `tool_calls`；
- 当模型认为任务已经达成、或者已拥有足够结论向人类汇报时，它会输出直接面向用户的文本答复，且**不再附带任何工具调用**（`len(tool_calls) == 0`）。
**因此，没有产生工具调用的纯文本回复，本身就是大模型对“任务已圆满完成”做出的自主判断！** 任何在中间强行掐断的固定轮次都是违反这一自主原则的。

### 2. 安全兜底 vs 任务限制的本质区别
取消人为固定轮次并不等于允许无限死循环。
在极端异常情况下（如模型遇到不可恢复的编译报错陷入死循环重试），系统必须存在一道底层的“安全熔断保险丝”（Runaway Safety Fuse，例如 100 轮），防止几千轮死循环烧光账户 Token。但这道保险丝属于极端防御，绝不能在正常的开发任务中成为常态化门槛。

---

## ③ 标准解决方案与实操步骤 (Actionable Solutions & Step-by-Step Guide)

### 1. 微内核上限退居防爆安全保险丝
在 `internal/core/loop/engine.go` 与 `llm_path.go` 中，将微内核执行上限由 24 调整为 100：
```go
// MCPCall/Verify 自 T1 起改为构造期注入，杜绝构造后改写（导出可变字段已移除，根治双构建隐患）
func NewExecutionEngine(reg *host.Registry, gw InteractionGateway,
    mcpCall func(ctx context.Context, name string, args map[string]any) (string, error),
    verify func(writtenFile string) (output string, pass bool)) *ExecutionEngine {
    return &ExecutionEngine{
        registry:    reg,
        gateway:     gw,
        mcpCall:     mcpCall,
        verify:      verify,
        maxSteps:    15,
        maxLLMTurns: 100, // 仅作为极端死循环防爆安全兜底保险丝
    }
}
```

### 2. 确立 0 工具调用即自主完成
在 `internal/core/loop/llm_path.go` 中，明确主路径退出逻辑：
```go
if len(toolReassembler) == 0 {
    // 模型未发起任何工具调用：模型自主判定当前任务已达成或已向用户交付最终回复
    break
}
```

### 3. 在 System Prompt 刚性注入自主完成契约
在 `internal/core/loop/strategy.go` 的 `ApplyStrategy` 中，统一注入自主执行规范：
```text
【任务自主完成与结束铁律】
1. 执行由你完全自主驱动，不设置人为固定轮次强行中断：当你自主判定当前任务已达成目标、或已得出明确结论向用户汇报时，请直接向用户输出答复内容，不要再发起任何工具调用。系统检测到你未发起工具调用时，即确认本次任务由你自主圆满交付。
2. 若任务尚未达成（如需要修改更多关联文件、继续运行测试排错、定位或修复代码等），请自主继续调用必要工具推进，直到任务完成。
```

### 4. TaskModel 适配自主模式
在 `app_chat.go` 中，将当前会话的任务预算设为 0（代表自主模式，不再进行扣减），完全按实际调用的 `ToolsUsed` 进行统计。

---

## ④ 避坑指南与最佳实践 (Troubleshooting & Best Practices)

1. **区分“自主判断”与“无限失控”**：赋予模型自主决策权的同时，必须保留用户侧的毫秒级无阻塞中断按钮（`■ Cancel`）以及底层的防爆熔断上限（100 轮）；
2. **严禁在模型产生工具调用时擅自截断**：只要模型还在输出工具调用，说明当前任务逻辑链条尚未封闭，任何人为轮次干预都会导致任务残缺半成品；
3. **保持状态机的一致性**：模型自主返回且测试通过、无待审查 Diff 时，系统自动将任务状态流转为 `completed`，无需用户再次手动确认。
