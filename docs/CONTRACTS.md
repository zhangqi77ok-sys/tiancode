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
| C-LLM-7 | 终态互斥且唯一：整流 EndReason != EndNone 的块恰好 1 个（EndNone 为零值=非终态） | `TestProviderStream_SingleTerminal` |

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
| C-SES-5 | 写入目标被占用（Windows rename 冲突）→ 备份式替换回退成功，原数据不丢。**保护对象已下移到存储层**：账本自 ADR-0002 起改为追加式（`O_APPEND` + fsync），不再有"全量改写 + rename"，该回退现在保护的是渠道配置与工具写文件 | `TestWriteFileAtomic_RenameConflictFallback` |
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

## C-CH：模型渠道管理

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-CH-1 | `config.json` 有值时首次启动迁移为一个激活渠道；空配置不迁移 | `TestStore_LoadMissingReturnsEmpty` / `TestMigrateFromConfig` / `TestChatService_MigratesConfigToFirstChannel` |
| C-CH-2 | 必填字段与协议校验；未实现协议**显式拒绝**；激活渠道不可删除 | `TestValidateChannel_RequiresCoreFields` / `TestFactory_UnsupportedProtocolExplicitError` / `TestChatService_ChannelCRUDValidation` |
| C-CH-3 | 切换激活渠道后下一次 Send 打到新上游（运行时重建生效） | `TestChatService_SetActiveRebuildsRuntime` |
| C-CH-4 | 模型发现失败报错且**不改动已保存配置**；UI 不清空已选模型 | `TestChatService_DiscoverModels` / `channels.test.ts: 模型发现失败不清空已有模型` |
| C-CH-5 | 渠道配置原子写（无临时文件残留） | `TestStore_SaveLoadRoundtripAtomic` |
| C-CH-6 | 密钥绝不出编排层：列表仅返回脱敏视图 + `hasKey` | `TestChannel_SanitizedHidesKey` / `TestChatService_ChannelCRUDValidation` |

## C-SES 扩展：会话删除 / 重命名 / 导出

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-SES-7 | 删除会话**幂等**（重复删除不报错），删除后不再出现在列表 | `TestDeleteSession_RemovesLedger` |
| C-SES-8 | 会话 ID 含路径分隔符一律拒绝（防越出账本目录读写删） | `TestDeleteSession_RejectsPathTraversal` / `TestSessionTitle_RejectsBadID` |
| C-SES-9 | 标题以账本事件（`session_renamed`）为事实源，重启后可恢复；空/超长标题拒绝 | `TestSessionTitle_ReplaysLatestRename` / `TestChatService_RenameSessionPersists` |
| C-SES-10 | 导出 Markdown 与界面同源（账本投影）；未重命名时标题回退会话 ID | `TestChatService_ExportMarkdown` |

## C-WS：工作区

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-WS-1 | 非法工作区（不存在/非目录/空白）拒绝，且**不改变当前值** | `TestChatService_SetWorkspace` |
| C-WS-2 | 切换工作区必须重建工具受控根与 agent（杜绝"界面切了实际没切"） | `TestChatService_SetWorkspace` + 装配统一走 `newRegistry` |

## C-APR：审批闸门（ADR-0007，默认关闭）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-APR-1 | 未配置审批清单时不干预（approver 为 nil，行为与历史版本完全一致） | `TestExecTool_NoApproverRunsDirectly` |
| C-APR-2 | 拒绝必须产生**模型可见**的失败结果（含工具名与原因），且工具零执行；无原因时补默认说明 | `TestExecTool_Denied` / `TestExecTool_DeniedWithoutReason` |
| C-APR-3 | 审批通道故障按**拒绝**处理（故障时放行最危险） | `TestExecTool_ApproverErrorDenies` |
| C-APR-4 | 等待审批受 ctx 约束：取消 → 视为拒绝，且未决请求被清理（不挂起、不泄漏） | `TestExecTool_CancelWhileWaiting` / `TestChatService_ApprovalCancelWhileWaiting` |
| C-APR-5 | 清单外工具直接放行且**不发事件**（不许泛化拦截，更不做内容分析） | `TestChatService_ApprovalBridge` |
| C-APR-6 | 未知或已处理的请求 ID 一律报错（不静默放行）；策略查询返回副本 | `TestChatService_ApprovalBridge` |
| C-APR-7 | 审批策略**持久化**：重装/重启后仍生效（开关关闭同样落盘，不得"关了又自己开"）；无渠道时也必须恢复（不许被提前返回跳过） | `TestChatService_ApprovalPolicyPersists` |

## C-INS：安装与卸载（M5）

> 为什么补这一组：安装器此前**没有任何契约条目**，于是"重建时漏掉桌面快捷方式"这类能力回退
> 无人拦下——遗留版创建了桌面 + 开始菜单两个入口（legacy `cmd/installer/main.go:217-222`），
> 重建版只剩开始菜单，用户装完在桌面找不到入口。契约缺位 = 可静默回退。

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-INS-1 | 安装创建**恰好两处**快捷方式：开始菜单 + 桌面，命名均为 `tiancode.lnk` | `TestShortcuts_BothStartMenuAndDesktop` |
| C-INS-2 | 卸载删除的快捷方式集合与安装创建的**逐项一致**（杜绝孤儿 `.lnk`）；且不因安装时用了 `-no-desktop-shortcut` 而漏删 | `TestShortcuts_InstallUninstallSymmetry` / `TestShortcuts_UninstallAlwaysCoversBoth` |
| C-INS-3 | 必需项策略：开始菜单快捷方式创建失败**中断安装**；桌面项失败只告警——恰好一个必需项 | `TestShortcuts_OnlyStartMenuIsRequired` |
| C-INS-4 | 卸载**不得删除其它安装的**卸载注册项：仅当 `InstallLocation` 指向本次卸载目录时才删；`-dir` 不一致时跳过告警 | `TestUninstallOwnsEntry` + `scripts/install-smoke.ps1 [3]` |
| C-INS-5 | 桌面路径按**注册表实际位置**解析（支持 OneDrive/组策略重定向），读不到才回退 `%USERPROFILE%\Desktop`；`%NAME%` 需展开 | `TestDesktopDirFallback_UsesUserProfile` / `TestExpandEnvVars` |
| C-INS-6 | 不依赖 PATH 解析 PowerShell：优先用 `%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe`，缺失才回退裸名 | `TestPowershellExe_ResolvesToExistingFile` / `TestPowershellExe_FallsBackWhenMissing` |
| C-INS-7 | 非致命问题（跳过桌面项、注册项归属不符、清理调度失败）必须**同时写 stderr 与 `%APPDATA%\tiancode\setup.log`**——GUI 子系统无控制台，只写 stderr 等于静默 | `scripts/install-smoke.ps1`（读取 setup.log 并逐行回显） |

## 契约变更记录

> 规则：契约一经发布不得静默变更（见 `STANDARDS.md` §4）。下表登记每一次**指向或表述**的修正——
> 即使行为语义不变也要记，否则会出现"测试名与实际实现层次长期不一致"这种慢性失真：
> 契约表声称覆盖了某行为，而该行为实际没有任何测试在看。

| 日期 | 契约 | 变更 | 依据 |
| --- | --- | --- | --- |
| 2026-09-23 | C-SES-5 | 锁定测试名由 `TestLedger_RenameConflictFallback` 更正为 `TestWriteFileAtomic_RenameConflictFallback`，并明确保护对象是**存储层原子写的回退路径**（渠道配置 / 工具写文件）。原表述是旧设计（全量改写 + rename）的残留，属指向修正，行为语义未变 | ADR-0002（账本改为追加式账本，不再全量改写） |
| 2026-09-23 | C-RT-4 | 锁定测试落地于 `internal/core/agent/agent_test.go::TestRuntime_FacadeBoundary`。此前该条约只有守卫 R1 的静态保证、没有同名测试。新测试做两件事：用替身运行时驱动内核跑完整一轮（编译期证明依赖的是接口而非具体实现），并扫描本包**非测试**源码，禁止出现适配器/编排/壳层 import | 本文件"ID 与测试名一一对应"的要求 |
| 2026-09-23 | C-APP-2 | 锁定测试落地于 `internal/app/chat_service_test.go::TestChatService_CancelKeepsEvents`，走 `httptest` 上游 + 真实 `chatRuntime` 的端到端路径。此前只有 `TestAgent_CancelKeepsEvents`，而它用 `fakeRuntime` **直接把 `EndCancelled` 喂进内核**，恰好绕过了真正会出错的那一环（runtime 中继把 ctx 取消误判为 `EndError`）——这个盲区正是"用户点中断却被上报成错误"长期未被发现的根因。补测同时修复了 `core/llm/runtime.go` 中继层的终态判定 | 本文件"ID 与测试名一一对应"的要求 + ADR-0003（流式三终态） |
| 2026-09-23 | **新增 C-INS-1 ~ C-INS-7** | 补齐安装/卸载契约（此前完全缺失）。同时修正实现两处：① 卸载按名字无条件删除共享注册项 → 改为校验 `InstallLocation` 归属（实测踩过：用隔离目录做卸载验证，连带删掉了用户正式安装的注册项）；② 非致命警告只写 stderr，而安装器是 `-H windowsgui` 构建、没有控制台 → 警告用户永远看不到，改为同时落盘 `setup.log` | 用户实测反馈"安装后桌面没有快捷方式" + legacy 对照（legacy 同时创建桌面与开始菜单） |
