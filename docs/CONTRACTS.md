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
| C-FS-5 | `list` 越界或非目录 → `IsError` 且工作区零修改 | `TestFSList_RejectsEscapeAndNonDir` |
| C-FS-6 | `list` 最多 500 条，超出截断并标注总数 | `TestFSList_OutputBounded` |
| C-FS-7 | `list` 只列下一层（子目录内容不出现） | `TestFSList_NonRecursive` |

## C-SEARCH：工作区内容搜索（M6）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-SEARCH-1 | 路径越界拒绝 | `TestSearch_PathEscapeRejected` |
| C-SEARCH-2 | 匹配数与总字节均有界，超限截断并标注 | `TestSearch_OutputBounded` / `TestSearch_OutputByteBounded` |
| C-SEARCH-3 | 无效正则 → `IsError` 且原因可读 | `TestSearch_InvalidPattern` |
| C-SEARCH-4 | 跳过二进制与内置忽略目录；显式 `path=vendor` 仍搜该根 | `TestSearch_SkipsIgnoredAndBinary` |
| C-SEARCH-5 | 零匹配成功，`Content` 含 `no matches` | `TestSearch_NoMatchIsSuccess` |
| C-SEARCH-6 | 超时预算内返回；有部分命中则带已捕获输出 | `TestSearch_TimeoutPartial` |
| C-SEARCH-7 | 每条命中带前后各 2 行上下文（命中行 `path:line:text`、上下文行 `path-line-text`）；相邻命中并块不重复；上下文计入 64KiB 预算，超限在**块边界**少给（不给半截块） | `TestSearch_ContextLines` / `TestSearch_ContextMerged` / `TestSearch_ContextCountsTowardByteBudget` |
| C-SEARCH-8 | `files_only=true` 只按工作区相对路径匹配、只返回路径（不读内容、不带行号、不受 1MiB 内容闸门约束）；忽略目录 / 越界检查 / 配额与内容搜索同一套 | `TestSearch_FilesOnlyByPath` / `TestSearch_FilesOnlyMaxMatches` |

## C-APP：编排纪律（M2 起持续生效）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-APP-1 | 持久化/流式任何错误必须上抛到 UI 层（守卫 R2 静态强制 + 用例测试） | `TestChatService_PersistErrorPropagates` |
| C-APP-2 | 用户中断 → `EndCancelled` 终态 + 账本保留已产生事件，UI 显示"已取消" | `TestChatService_CancelKeepsEvents` |
| C-APP-3 | `Replay` 投影含 tool 卡（name/status/content）与 assistant thinking | `TestChatService_ReplayIncludesTools` |
| C-APP-4 | **零块终态必须可见**：整回合没有任何增量到达就失败时（无可用渠道 / 流未建立即失败），错误必须新建一条错误气泡呈现，禁止因"没有进行中的助手气泡"而静默丢弃——否则界面表现为"消息发出去了、什么都没发生" | `chat.test.ts: 零块错误终态必须新建可见错误气泡` / `chat.test.ts: 零块空闲超时也可见` / `chat.test.ts: 零块正常终态不补空气泡` |

## C-AGT：模型上下文回放（M6）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-AGT-1 | 第二轮 `Run` 的请求消息含上一轮 `assistant(tool_calls)` + `role=tool`，ID 配对 | `TestAgent_DerivesToolHistoryAcrossTurns` |
| C-AGT-2 | 单条 tool 结果 >4096 字节时模型侧截断并含 `truncated` 标注；账本仍是全文 | `TestAgent_TruncatesToolResultForModel` |
| C-AGT-3 | 无对应 result 的 tool_call 不进入模型消息 | `TestAgent_OmitsUnpairedToolCall` |
| C-AGT-4 | 缺 `id` 的旧事件能合成 ID 并配对，第二轮请求协议合法 | `TestAgent_SyntheticIDsForLegacyLedger` |

## C-CH：模型渠道管理

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-CH-1 | `config.json` 有值时首次启动迁移为一个激活渠道；空配置不迁移 | `TestStore_LoadMissingReturnsEmpty` / `TestMigrateFromConfig` / `TestChatService_MigratesConfigToFirstChannel` |
| C-CH-2 | 必填字段与协议校验；未实现协议**显式拒绝**；激活渠道不可删除 | `TestValidateChannel_RequiresCoreFields` / `TestFactory_UnsupportedProtocolExplicitError` / `TestChatService_ChannelCRUDValidation` |
| C-CH-3 | 切换激活渠道后下一次 Send 打到新上游（运行时重建生效） | `TestChatService_SetActiveRebuildsRuntime` |
| C-CH-4 | 模型发现失败报错且**不改动已保存配置**；UI 不清空已选模型 | `TestChatService_DiscoverModels` / `channels.test.ts: 模型发现失败不清空已有模型` |
| C-CH-5 | 渠道配置原子写（无临时文件残留） | `TestStore_SaveLoadRoundtripAtomic` |
| C-CH-6 | 密钥绝不出编排层：列表仅返回脱敏视图 + `hasKey` | `TestChannel_SanitizedHidesKey` / `TestChatService_ChannelCRUDValidation` |
| C-CH-7 | **自动禁用是运行期标记**：应用启动时把 `auto_disabled` 渠道恢复为 `enabled`（单次网络抖动 / 上游 5xx 不得让应用永久无渠道可用）；`manually_disabled`（用户显式意图）与凭证级禁用标记**一概不动**；恢复动作留痕（启动日志） | `TestPool_ReviveAutoDisabled` / `TestPool_ReviveKeepsManualDisabled` / `TestPool_ReviveKeepsCredentialBan` / `TestChatService_StartupRevivesAutoDisabledChannel` |
| C-CH-8 | 无可用渠道的错误**必须可执行**：点名渠道与其状态（自动禁用/手动停用）、缺失的模型名与渠道现有模型，并给出恢复动作；禁止只回"无可用渠道：default / m（retry=0）"这类零信息量文本（该错误直接渲染在对话气泡里） | `TestGateway_NoChannelErrorIsActionable` / `TestGateway_NoChannelErrorNamesMissingModel` |

## C-UI：对话界面（0.2.24）

> 悬浮任务清单（用户反馈"任务清单最好是悬浮在这里，点击就展开一个悬浮列表，也可以关闭成一个悬浮图标"）。
> 为什么单列一组并写明：任务清单的**载体**从"消息流内的内联卡"变成"对话面板上的悬浮件"，
> 这是渲染契约的变化——数据契约（账本 EventTodo / 实时 TodoEvent / 单卡原地更新）完全不变。
> 内联卡消失后，若没有契约盯着，很容易出现"两处都在渲染"或"两处都不渲染"的静默回退。

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-UI-1 | 任务清单**不在消息流内联渲染**（不再随对话滚走）；由悬浮件承载，点击在"悬浮列表 ⇄ 悬浮图标"两态间切换；无任务清单时**不渲染**（不是空壳） | `floatingTodo.test.ts: 无任务卡时返回 null` + 无头渲染实证（两态截图） |
| C-UI-2 | 悬浮件几何：位置钳制在对话面板内且**至少保留 56px 可见**（拖不出视野）；元素比 56px 还小时整体留在容器内；上界不为负 | `floatingTodo.test.ts: 位置钳制…/元素大于容器时…` |
| C-UI-3 | 持久化位置解析对脏数据**一律回退默认位**（缺失/截断 JSON/非数字/非有限数），绝不因脏数据渲染出错或抛异常 | `floatingTodo.test.ts: 解析持久化位置…` |
| C-UI-4 | 任务快照取**最后一条** todo 卡（实时与账本重放同源、同卡原地更新）；拖动有阈值判定，**点击不被手抖吞掉**；新的一份清单出现时展开 | `floatingTodo.test.ts: 任务快照取最后一条 todo 卡/拖动判定有阈值` |

### 多会话并行对话（0.2.25）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-APP-5 | **多会话可并行运行**：不同会话可同时各跑各的轮次（每轮独立构建 agent；`agent.Loop` 的 phase 不再是全应用互斥锁）；上游等两路请求到齐才放行以断言并行性；各会话账本互不串写 | `TestChatService_ConcurrentSessionsRunInParallel` |
| C-APP-6 | 审批/问答事件**必须携带 sessionID**（随轮注入 uiApprover/uiAsker）：后台会话的审批卡/问答卡归位到发起它的会话，绝不插进当前视图 | `chat.test.ts: 审批卡按 sessionID 归位到后台会话` |
| C-UI-5 | 前端运行态**按会话隔离**：流式/工具/todo/终态事件按 sessionID 路由进各自缓冲（后台会话不再被丢弃）；切回有缓冲的会话**不重放覆盖**；运行中允许切换/新建会话；删除运行中的会话必须显式拒绝且错误可见；队列续发仍发给原会话 | `chat.test.ts: 后台会话的增量落在它自己的缓冲里…/后台会话的终态不丢也不串…/删除运行中的会话被拒绝且错误可见/后台会话的队列续发仍发给它自己/切换回已有缓冲的会话不重放覆盖` |
| C-UI-6 | 会话 ID **进程内唯一**（含毫秒与计数器）：ID 即账本文件名，同秒重复会让两个会话串写同一份账本 | `chat.test.ts: 后台会话的增量落在它自己的缓冲里…`（两会话不同 ID 才能通过） |

### 交互输入与强制工具（0.0.12）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-UI-7 | 出错不再"重发原文"：助手气泡**没有**「重试上一问」；用户气泡「重跑」只把原文与附件回填输入框（可改），确认后才 `RerunFrom` 并发送**输入框里的文字**（取消则什么都不撤回）；用户气泡「复制」复制该消息正文 | `MessageBubble.test.ts`（重跑只回填不发消息 / 无重试按钮 / 复制）；`Composer.test.ts`（确认后发送改过的文字 / 取消不撤回不发送） |
| C-UI-8 | 工具卡「复制输出」复制当前展示全文（有 diff 给 diff）；对话区选中文字可「放进输入框」（追加，不替换草稿、不自动发送） | `ToolCard.test.ts`（命令卡/写卡复制输出）；`appendSelection.test.ts` |
| C-APP-7 | 强制工具：只允许 `skill` / `mcp`（白名单），本轮**模型开口前**先调用一次，结果按普通工具调用/结果落账本并进上下文；失败**停轮**（零后续模型请求）；未指定时行为与旧版一致 | `forcedtool_test.go`（ForcedSkillCalledFirst / ForcedMCPCalledFirst / ForcedToolFailureStopsTurn / NoForcedToolUnchanged / ForcedToolWhitelist）；`Composer.test.ts`（/ 选技能、/ 选 MCP） |

### C-EXT：扩展自管理（0.2.26）

> 用户需求："让 AI 添加 mcp、skill 数据时能让 AI 自己添加"。此前扩展只能用户手动配，
> 模型的 fs 工具又被工作区约束（extensions.json 在 %APPDATA%，越界即拒绝）。

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-EXT-1 | 模型可经 `ext_manage` 增删 MCP/Skill，与设置面板走**同一条存储路径**（同一 catalog.Store + 保存后关闭旧 MCP 会话）：AI 改的、界面看到的、下一轮注入前言的，是同一份事实源 | `TestManage_McpAddPersistsAndProbes` / `TestManage_McpRemoveAndUnknown`（落盘钩子计数） |
| C-EXT-2 | `mcp_add` **现场连接验证**并把该服务器公布的工具带回给模型；验证失败**不回滚**（配置已保存），错误原文可见 | `TestManage_McpAddPersistsAndProbes` / `TestManage_McpAddKeepsConfigWhenProbeFails` |
| C-EXT-3 | 重名 / 缺 command(url) / 删除不存在 → 报错必须**可执行**（引导先 list 确认） | `TestManage_McpAddRejectsDuplicateAndMissing` / `TestManage_SkillAddRemoveRoundtrip` |
| C-EXT-4 | 前言提示 `ext_manage` 的存在（模型发现路径），并告诫"用户没要求时不要擅自增删" | `TestPrefaceMentionsSelfManage` |

## C-SES 扩展：会话删除 / 重命名 / 导出

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
| 2026-09-27 | **新增 C-AGT-1~4 / C-FS-5~7 / C-SEARCH-1~6 / C-APP-3** | M6 编程智能体可用性：跨轮工具历史回放（含模型侧 4096 字节截断、旧账本合成 ID）、`fs.list`、工作区 `search`、Replay 投影工具卡与 thinking。截断只发生在 derive 视图，账本保留全文（ADR-0008；编号避让远端已发布的 ADR-0007 审批闸门） | `docs/superpowers/specs/2026-09-27-coding-agent-usability-design.md` + ADR-0008 |
| 2026-09-28 | **新增 C-CH-7 / C-CH-8 / C-APP-4** | 实机故障：唯一渠道被 `auto_ban` 置为 `auto_disabled` 后，应用表现为“发消息毫无反应、没法对话”。三处根因分别固化为契约：① 选路失败的错误文本零信息量（用户不知道是没配、被禁用还是模型名错）→ C-CH-8 要求可执行；② `auto_disabled` 被持久化却无任何自动恢复途径 → C-CH-7 定为运行期标记、启动重新评估；③ 零块终态时前端 `onTerminal` 找不到“进行中的助手气泡”，错误被静默丢弃 → C-APP-4 要求新建错误气泡。同时修复：`gateway.noChannelError` 翻译错误、`channels.ReviveAutoDisabled` 新增、`chat.ts onTerminal` 补零块分支 | 用户实测反馈“不对哇，没法进行对话，这块做的有问题” + 复现证据（`TERMINAL endReason=2 err=无可用渠道：default / grok-4.7（retry=0）`，账本仅 `user_message` 无任何助手事件） |
| 2026-09-28 | **新增 C-UI-1 ~ C-UI-4** | 任务清单的呈现载体变更：由"消息流内联卡（`TodoCard`，随对话滚走）"改为"对话面板上的悬浮件（`FloatingTodo`：悬浮列表 ⇄ 悬浮图标，两态可拖动、位置与折叠态跨重启保留）"。数据契约不变（账本 `EventTodo` / 实时 `TodoEvent` / 单卡原地更新）；`groupMessages` 仍产出 `kind='todo'` 分组但两处渲染器都刻意跳过它（改由悬浮件渲染），`TodoCard.vue` 删除 | 用户反馈"任务清单最好是悬浮在这里（贴截图），点击就展开一个悬浮列表（可以拖动移动），也可以继续关闭成一个悬浮图标（可以拖动移动）" |
| 2026-09-28 | **新增 C-APP-5 / C-APP-6 / C-UI-5 / C-UI-6** | 多会话并行对话（用户反馈："一个会话在运行就其他的没法操作"）：后端 agent 改为每轮独立构建（原 `agent.Loop.phase` 是全应用单轮互斥锁）；审批/问答事件带 sessionID 归位；前端运行态按会话隔离（事件路由、切换不重放、删除运行中会话显式拒绝、队列续发归属原会话）。顺带修一个测试抓到的真 bug：会话 ID 精确到秒，同一秒新建的两个会话共用一个账本文件 | 用户需求 + `TestChatService_ConcurrentSessionsRunInParallel`（上游等两路到齐，串行必超时） |
| 2026-09-28 | **新增 C-EXT-1 ~ C-EXT-4** | 新工具 `ext_manage`：用户让 AI"添加 mcp、skill 数据"时，AI 自己完成配置（与设置面板同一条 catalog.Store 存储路径 + 保存后关闭旧 MCP 会话）；mcp_add 现场连接验证并带回工具清单，验证失败不回滚；重名/缺参报错可执行 | 用户需求 |
| 2026-09-30 | **移除文件写入确认链路（原 0.0.10 EditGate）**；`fsTitle` 改工作区相对路径；新增右侧文件详情面板 | ① write/replace 不再需要"应用/跳过"（改了就是改了）：`chatEditGate`/`EditEvent`/`ResolveEdit`/`chat:edit` 事件与前端确认卡全链路删除；代码块「应用到文件」改直写并同步返回回执（`fstool.ProposeWrite` → `ProposeWriteResult`，不进账本故不提供撤销）；工具级审批（ADR-0007，默认关）不受影响。② 工具卡/审查带主标签由末段改为**相对路径**（`TestFSTitle_RelativePath`）：多目录同名文件（agent.go）可区分；审查带默认折叠 + 按文件聚合（"改 N 次" + 累计 ±），行点击开右侧详情。③ 点文件行/工具卡文件名 → 右侧 `FileDetailPanel`（逐次 diff + 恢复写入前 + 资源管理器）；Esc 改为**浮层优先消费**（`composables/useEsc.ts` 注册栈，`useEsc.test.ts`），关浮层不再顺手中断生成 | 用户反馈："修改代码不要有应用和全部应用的功能，改了就是改了"、"本轮变更列表默认折叠、显示具体文件名而不是统一叫 agent.go"、"修改的文件要有详情，点击后右侧弹出一区块" |
| 2026-09-30 | **新增 C-UI-7 / C-UI-8 / C-APP-7**；输入框指定技能与 MCP | ① **不出错重发**：助手气泡删掉「重试上一问」（它只把上一问原文再发一遍，失败回合仍留在流里）——出错只保留「从这条用户消息重跑」；用户气泡加「复制」（剪贴板失败走 toast，不假装成功）。② **改字再重跑**：「重跑」只把原文与附件交回输入框（光标在文末、附件进待发送区），用户改完按发送时才确认分叉 → `RerunFrom`（撤回其后文件改动 + `EventFork` 分叉，旧行不改写）→ 发送**输入框里的文字**；取消确认则什么都不撤回（附件从当前前端消息上取，不动账本格式）。③ **工具卡复制输出**：复制当前展示的全文（有 diff 给 diff，否则原始输出；含搜索结果与命令输出），路径仍走 `OpenInDefaultApp` 且不定位到行。④ **选中文字放进输入框**：对话区（`data-conversation`）有非空选区时，输入框旁出现「放进输入框」，把选区**追加**到草稿末尾（已有内容空一行），不替换草稿、不自动发送。⑤ **队列按钮更正**：`promoteQueued` 的图标由 `send` 换成不表示发送的箭头，title/aria-label 统一为「提前：本轮结束后最先发出」（行为仍是置顶，不另开一轮）。⑥ **强制工具（`forced` 字段）**：`/` 菜单只列**已启用**的技能与 MCP 服务器；选中后程序在本轮**模型开口前**先调用现有 `skill`（参数 `name`）或 `mcp`（参数 `server`/`tool`/`arguments`）工具，结果按**普通工具调用/结果**落账本并进上下文（菜单里不留命令字样，模型改不了服务器名/工具名）；一条消息最多一个；MCP 工具名未知时用 `tool=list` 拉清单（不另写发现协议），参数用户填了就原样传入、没填则 `{}` 交工具自己报缺参；`ext_manage` 不进菜单（增删仍只在侧栏，审批不变）；强制调用失败**本轮停在这条工具错误上**（零后续模型请求，不假装用过）；白名单之外的名字一律拒绝（强制调用不得成为绕过模型与审批直接执行 fs/shell 的口子）；未指定时行为与旧版一致 | 用户反馈："出错不要再发一遍"、"改几个字再重跑，并能复制自己的话"、"工具卡输出能复制"、"选中文字放进输入框"、"队列里的发送图标不要再叫发送"、"在输入框里直接用技能和 MCP" |
| 2026-09-30 | **新增不变式：壳层发送路径必须消费 ChatService 返回的流** | 实机故障"上传文件或图片后发出去没有回复"的真因：`app/app.go` 的 `SendWithAttachments` 把 `chat.SendWithAttachments` 返回的流通道用 `_` 丢掉——**没人消费** → 服务转发循环卡在 `out <- c` → 内核卡在第二块（账本只留一条增量、界面永远"正在思考"、90 秒后被零事件看门狗当"上游黑洞"收掉，日志里连上游超时都没有）。文本路径走 `Send`（有消费循环）所以只有带附件这一种表现。**不变式**：任何调用 `ChatService.Send*` 的路径都必须消费返回通道到关闭（两条发送路径共用 `drainTurn`；`app/send_drain_test.go` 用结构锁 + 端到端用例（伪造上游 SSE → 真实 ChatService → 事件桥替身）锁住，丢掉通道立刻挂测试）。诊断过程与证据：裸 HTTP 探针、自家 provider、真实 ChatService 三段都能拿回完整回答（418~1046 块、1259 字），唯独 GUI 那条卡死——所以问题不在请求侧、不在中转、不在解析器 | 用户实机反馈（连续两轮截图）"还是不行哇，你有没有真实测过" + 应用日志（`upstream request bytes=97599/130679` → `connected status=200` → `watchdog fired idle=1m30s`，其间账本只有一条 `assistant_delta`） |
| 2026-09-30 | **附件轮消息形态（四协议一致）**：Content 与 Parts 同源，图片只内联不转公网 | 实机故障"上传文件或图片后发出去没有回复"。根因链已逐条核对：① `derive.go` 只在有附件时填 `llm.Message.Parts`、**Content 留空**；② OpenAI 兼容读 Parts ✓，但 `anthropic.go` 的用户消息只读 `m.Content`（空 → 不生成块 → 空块被 flush 丢掉 = 这轮没发给模型），`codex.go` 的 `input_text` 直接取 `m.Content`（空串）；③ 前端 `chat.ts` 在"正常结束且无正文无思考"时删掉占位 → 表现为"发出去就没了"。**新契约**：① 派生层附件轮 **Content 与 Parts 都写、同源**——Content = 用户原文 + 内联文件正文 / 未内联的路径说明 + 每张图只写「文件名：图像见多模态部分」；**图片 base64 绝不进文字**；Parts = 文字段 + image_url data URL（片段带 `Name`，json:"-" 不上线）；无附件仍走纯字符串 content；估 token 时两者只算一处。② 图片一律**内联在请求体里**（本工具是本地开源工具，不转 http(s) 链接）；中转只收 http(s) 时适配器写明该图未发送。③ Anthropic：用户消息读 Parts（text → text block、data URL → base64 image block），无 Parts 才回落 Content；转换后**一个块都没有就报错**，不静默省略这条用户消息。④ Codex：`input_text` 先取 Parts 文字再回落 Content，保证非空；该链路没有图片字段，**不发明** `input_image`，在文字里写「本协议未发送图像：文件名」。⑤ OpenAI 兼容行为不变（仅加测试锁住）。⑥ 界面：每个附件都是可点名字（文件与图片一致），点开本机详情（只显示消息上已有字段：名/类型/内联方式/本机路径；图片用现成 dataUrl 显示原图；读不到就写"文件已不在本机"），Esc 与点外面关闭走现有浮层栈；带附件的一轮正常结束却无正文时**保留占位**并写"模型没有返回内容" | 用户反馈（截图 + 事实清单）："对话上传文件或图片后发出去没有回复" |
| 2026-09-30 | **改写上下文预算的适用范围（C-AGT-5 修订）**：默认预算只作折叠阈值，不阻断回合 | 0.0.23 实机回归（用户截图）：超长会话（估算 58k/79k tok）在 32k **默认预算**下被判"折叠旧内容后仍装不下"→ 直接失败，用户没法继续这场对话。**修订后的规则**：① 折叠阈值来源分两种——渠道/用户**声明**的 `contextLimit`（取启用渠道最小值）与**我们兜底的默认值**（未声明时 32k）；② **声明上限**折完仍超 → 沿用 0.0.11：不发请求，给明确终态（发出去必被上游按长度拒，还会把本地预算问题伪装成上游错误）；③ **默认值**折完仍超 → **照发**：默认值不是用户定的限制，拿它阻断回合等于惩罚"没填 contextLimit"；折叠照做（体量已压到最小），油表标"已尽量折叠"而不是"已达上限"，上游真装不下会自己报错（可见、可归因——超时文案带本轮附件体量）。④ 油表在默认预算下继续写明"按默认预算"，不得让人以为渠道里填过这个数 | 用户实机反馈："这个功能有问题，应该是限制一次对话上传的文件数量，而不是限制大小吧？你也没必要管上下文上传了多少图片或者文件"（截图：`估算约 58048 tok 超过默认预算（32768 tok）`） |
 ① 每条命中带前后各 2 行上下文（`contextLines=2`）：命中行仍是 `path:line:text`、上下文行 `path-line-text`（rg 的 :/- 约定，一眼分辨哪行是命中），上下文计入 64KiB 预算、超限按块边界少给——此前只有命中那一行，模型定位后常要再 `read` 一次，每次定位多一轮往返。② `files_only=true` 按工作区相对路径匹配、只返回路径列表（不读内容、不受 1MiB 内容闸门约束）——此前"按名字找文件"只能靠 `glob`（内容搜索的过滤器）或 `tree` 逐层翻。③ 前端：搜索结果行与 `read` 卡片的路径可点，走既有 `OpenInDefaultApp`（**只打开文件**；行号不跳转，也不假装能跳）。**刻意未动**：read-后-才允许写、replace 多处匹配拒绝（防覆盖未读内容）、`tree` 深度、diff 的 `@@` 行号 | 用户反馈："搜索只有命中那一行，没有上下文"、"没有按文件名找"、"搜索结果点不了" |
| 2026-09-30 | **新增回合检查点：撤回本轮 / 从这条用户消息重跑 / 账本合批 / 上下文治理 / 超时分层 / 步数分段 / 建流退避 / preface 幂等** | ① 账本合批刷盘：AssistantDelta 满 64KB 或 100ms 才 fsync，其余事件（用户消息/工具调用与结果/todo/助手锚点/终态）一律立即 fsync（先冲攒批）；新增 `Ledger.Flush`，agent 的 `emitTerminal` 兜底刷盘——**修掉取消路径丢 delta 的回归**（`TestAgent_CancelKeepsEvents` 抓到）；`Replay`/`Close` 前先刷（read-your-writes）。② 上下文治理：渠道 `contextLimit`（token，池取已启用渠道最小值），估算口径 4 ASCII 字符≈1 token、1 非 ASCII 字符≈1 token，图片 1500 token/张；达预算 85% 起分级折叠（旧图片→路径说明、两轮以前 shell/写回执→一行摘要、只读窗口收窄），用户原话不删、最近一轮全文保留，超限置 `dropped` 并经 `chat:context` 上报（油表显示剩余比例与折叠项）。③ 超时分层：`TimeoutBudget{FirstByte 3min, Total 30min}`，首字节独立看门狗；终态文案写明"本地预算用尽/等待首个数据超过 X"（绝不伪装上游错误）。④ 步数分段：25 步用尽**先询问**（可取消），同意续跑、拒绝/取消正常收尾（TurnEnd 落账）。⑤ 建流重试退避 200/500ms（换凭证走 gateway 既有 Select 轮询）。⑥ preface 与上一手相同不替换 system（prompt cache 幂等）。⑦ **回合检查点**：轮次内首次修改某文件前的快照（`tools.RoundCheckpoint`，fstool 收集，32MB 上限、超限记 Note）随轮次收尾落账本（`round_checkpoint`）；「撤回本轮」按检查点整批恢复（当前内容哈希不符则跳过，绝不覆盖用户改动），撤回动作落 `round_revert`；「从这条用户消息重跑」撤回其后文件改动 + 追加 `fork{from_seq}`（派生/投影丢弃 [from_seq, fork] 区间，**旧行永不改写**），目标消息本身也在丢弃范围内（重跑重新落一条同文本消息，避免模型看到两条重复输入）；`ProposeFileWrite`（代码块应用到文件）写入成功落 `user_edit` 账本事件——Replay 投影为工具卡（重启后仍在）、派生历史注入一句"用户已手动应用过"；用户消息投影携带 `seq` 作为重跑锚点 | 用户六块需求（性能与上下文治理批次）+ 上一条"应用到文件不进账本"缺口的收口 |
