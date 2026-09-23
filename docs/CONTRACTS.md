# 行为契约（CONTRACTS）

> 每条契约 = 一句可测语句，ID 与测试名一一对应（`Test<Module>_<Behavior>`）。
> 契约一经发布不得静默变更；变更必须走 ADR 并同步修改测试。新模块落地同 PR 登记于此。

## C-LLM：流式三终态（M2，参照 new-api stream_scanner.go）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-LLM-1 | 上游正常完成（`[DONE]` 或 finish_reason）→ 恰好一个 `EndDone` 终态块，随后 channel 关闭 | `TestProviderStream_DoneTerminals` |
| C-LLM-2 | 上游挂起（连续 idleTimeout 无新数据）→ `EndIdleTimeout` 终态块，不永久阻塞 | `TestProviderStream_IdleTimeout` |
| C-LLM-3 | ctx 取消 → `EndCancelled` 终态块；实现无 goroutine 泄漏、无连接悬挂 | `TestProviderStream_Cancelled` |
| C-LLM-4 | 流内上游错误报文 → `EndError` 终态块且 `Err` 携带可读原因（fail-closed，不吞） | `TestProviderStream_UpstreamError` |
| C-LLM-5 | 连接中断（scanner 错误）→ `EndError` 终态块，不静默结束 | `TestProviderStream_ConnReset` |
| C-LLM-6 | 消费方停止读取 → 发送方经 select 逃生退出（`ctx.Done`），不永久阻塞在 channel 发送 | `TestProviderStream_SlowConsumerEscape` |
| C-LLM-7 | 终态互斥且唯一：整流 EndReason 非零块恰好 1 个 | `TestProviderStream_SingleTerminal` |

## C-RT：模型调用运行时（M2，ADR-0005；纪律借 new-api"流前重试、流中不换渠道"）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-RT-1 | 流开始前的失败（连接失败/HTTP 非 2xx）按 RuntimePolicy.MaxAttempts 重试，对 agent 透明 | `TestRuntime_PreStreamRetry` |
| C-RT-2 | 首块发出后的失败**不重试、不换渠道**，直接透传上游 EndReason 终态（防上下文撕裂） | `TestRuntime_NoRetryMidStream` |
| C-RT-3 | Runtime 施加连接/空闲/总时长三层超时预算；ProviderPort 无法绕过（经构造注入） | `TestRuntime_TimeoutBudget` |
| C-RT-4 | agent 只依赖 ChatRuntime，不感知渠道与重试的存在（Facade 边界，守卫 R1 静态保证） | `TestRuntime_FacadeBoundary` |

## C-SES：事件账本与会话恢复（M1）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-SES-1 | 追加成功 = 事件行已 fsync 落盘；此后内存状态才推进（write-ahead，账本即事实源） | `TestLedger_AppendBeforeAdvance` |
| C-SES-2 | 重放按 Seq 严格有序，结果与写入顺序一致 | `TestLedger_ReplayOrder` |
| C-SES-3 | 账本尾部半行（写入中断）→ 重放自动截断该行，前面事件完整恢复 | `TestLedger_ReplayTornTail` |
| C-SES-4 | 追加失败（磁盘/rename 冲突）→ 错误必须返回调用方，禁止吞掉 | `TestLedger_AppendErrorPropagates` |
| C-SES-5 | Windows rename 冲突（目标被占用）→ 备份式替换回退成功，原数据不丢 | `TestLedger_RenameConflictFallback` |
| C-SES-6 | 轮内崩溃 → 重放恢复到最后一条完整事件（`assistant_message` 锚点），不丢整轮已确认内容 | `TestLedger_CrashReplayRecovery` |

## C-TOOL：工具执行契约（M3/M4）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-TOOL-1 | 所有可变工具在实现内部施加超时（默认 120s，可配），必在超时预算内返回 | `TestShellRun_TimeoutReturns` |
| C-TOOL-2 | 超时/中断 → 返回已捕获的**部分输出** + `TimedOut=true`，不静默 | `TestShellRun_TimeoutPartialOutput` |
| C-TOOL-3 | 业务失败 → `ToolResult.IsError=true` 且 Content 为可读原因；`error` 返回值仅用于机制故障 | `TestShellRun_BusinessError` |
| C-TOOL-4 | 后台模式日志缓冲有界（超限截断并标注），无内存无界增长 | `TestShellRun_BackgroundLogBounded` |
| C-TOOL-5 | 取消（ctx.Done）→ 立即返回 `TimedOut=true` 终态，进程树被终止 | `TestShellRun_CancelKillsTree` |

## C-FS：文件编辑正确性（M3）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-FS-1 | write 原子落盘：任何时刻观察者看到的文件要么是旧内容要么是新内容，无中间态 | `TestFSWrite_Atomic` |
| C-FS-2 | replace 多处匹配 → 报错且**文件零修改**（多处匹配阻断） | `TestFSReplace_MultiMatchBlocks` |
| C-FS-3 | replace 零匹配 → 报错且文件零修改 | `TestFSReplace_NoMatchBlocks` |
| C-FS-4 | 路径越界（`../`、绝对路径逃逸工作区）→ 拒绝执行 | `TestFSWrite_PathEscapeRejected` |

## C-APP：编排纪律（M2 起持续生效）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-APP-1 | 持久化/流式任何错误必须上抛到 UI 层（守卫 R2 静态强制 + 用例测试） | `TestChatService_PersistErrorPropagates` |
| C-APP-2 | 用户中断 → `EndCancelled` 终态 + 账本保留已产生事件，UI 显示"已取消" | `TestChatService_CancelKeepsEvents` |
