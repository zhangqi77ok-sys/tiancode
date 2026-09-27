# ADR-0008：工具结果在模型视图截断，账本保留全文

日期：2026-09-27 ｜ 状态：已接受

## 背景

跨轮把 `tool_result` 回放进模型上下文后，单次 shell/git/search 输出可达数十 KB，长会话会线性撑爆窗口。若在写入账本时截断，导出、UI 展开和审计会永远失去原文。

## 决策

1. 账本 `tool_result.content` 存全文。
2. `deriveMessages` 发给模型时按 4096 字节截断（UTF-8 符文边界）并标注 `[truncated, original N bytes]`。
3. UI IPC 另限 64KiB，与模型预算独立。

## 后果

- 旧会话重放自动享受同一预算，不必改写 JSONL。
- 同轮多步 ReAct 仍使用内存中的全文（截断只发生在跨轮 derive）。
- 契约 C-AGT-2 锁定此行为。
