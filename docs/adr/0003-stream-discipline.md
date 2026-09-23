# ADR-0003：流式调用强制三终态契约（EndReason）

日期：2026-09-23 ｜ 状态：已接受

## 背景

旧实现（`plugins/provider/openai/openai_provider.go:261-281`）：

- `scanner.Scan()` 阻塞无空闲看门狗、无总超时——上游挂起 = 永久卡死（用户实测"断流卡死"）；
- `outChan <-` 发送阻塞无逃生，消费方卡住即 goroutine 泄漏 + 连接悬挂；
- `scanner.Err()` 未生产性处理，连接中断静默结束；
- 前端无法区分"正常结束/出错/挂起"。

## 决策

流式 API 以 **EndReason 互斥终态**为第一契约（参照 new-api `relay/helper/stream_scanner.go` 的 EndReason 追踪）：

- 四态：`EndDone` / `EndError` / `EndCancelled` / `EndIdleTimeout`；整流恰一个终态块，其后 channel 关闭；
- 实现侧强制：空闲看门狗（每收到数据重置）、发送逃生（select ctx.Done）、ctx 取消传播、终态后 `Body.Close()` 反压上游；
- `EndIdleTimeout` 独立于 `EndError`：挂起应提示重试，错误应展示原因，处置不同。

## 后果

- 契约测试 C-LLM-1~7 全部以 httptest 故障时序锁定（挂起/中断/慢消费/流内错误）；
- 前端 UI 围绕三终态设计状态显示（M2），不再猜测流是否结束；
- 该契约属 core/llm 包级文档内容，适配器实现不得绕过（arch_check R1 保证适配器只能经端口接入）。
