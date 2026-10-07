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
| C-RT-5 | prompt cache（0.0.41）：Anthropic 适配器请求在 system 块与最后一条消息的最后一个块打 `cache_control`（覆盖 tools+system 稳定前缀与整段历史）；OpenAI 兼容协议依赖上游自动前缀缓存，不发送标记 | `TestAnthropic_ConvertRequest`（缓存点断言） |

## C-SUB：只读子代理（0.0.41）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-SUB-1 | task 子代理在临时目录的一次性账本里运行，跑完即删——绝不写主会话账本、不进侧栏 | `TestSubagent_RunsIsolatedAndReturnsReport` |
| C-SUB-2 | 写路径结构性排除：子代理工具集经只读闸门包装，`fs` 写类 action 与 `memory` 写 action 一律拒绝且原因可读（回给子代理模型）；search/git/webfetch 整体只读 | `TestSubagent_WriteActionsRejected` |
| C-SUB-3 | 子代理最终回复作为 task 工具结果交还主对话（Title/Op 语义标签在案），报告有界（头尾保留） | `TestSubagent_RunsIsolatedAndReturnsReport` |
| C-SUB-4 | 主轮取消经 ctx 传播，子代理以取消终态收束为可读业务失败；task 自带 15min 硬超时（C-TOOL-1） | `TestSubagent_CancelPropagates` |
| C-SUB-5 | 子代理工具集里没有 task（不递归）、没有 shell/browser 写面；`task` 在内核只读白名单内（可 fan-out 并行） | `TestSubagent_RegistryHasNoTaskAndWrapsReadonly` |

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
| C-FS-8 | write/replace 成功后对 `.go`（工作区含 go.mod）自动编译诊断（go vet，含 _test.go），错误内联进当次回执；诊断超时/跳过**绝不改写写入的成功语义**，干净时静默 | `TestWrite_AutoDiagnoseInline` / `TestWrite_AutoDiagnoseSilentOnClean` / `TestWrite_AutoDiagnoseSilentNonModule` |
| C-FS-9 | 手动 `fs.diagnose`：干净明说"通过"、跳过明说原因（非 Go/非 module/testdata、vendor）、错误带可点 path:line（工作区相对、正斜杠） | `TestDiagnoseAction_Manual` / `TestDiagnose_BrokenAndClean` / `TestDiagnose_GracefulSkips` |
| C-FS-10 | `fs.symbols` 大纲有界（≤300 条）：Go 走 parser（签名=源码原文切片，坏函数 AST 名字兜底）；其余扩展名正则启发式**必须标注**；无引擎的扩展名显式报错 | `TestSymbols_GoOutline` / `TestSymbols_RegexFallbackAndUnknown` / `TestSymbols_Bounded` |
| C-FS-11 | vet 输出解析只认 `file:line[:col]`（exe 前缀剥离、盘符路径重组、`./` 归一），认不出的行宁可漏报不误报 | `TestParseVetLine` / `TestDiagnose_BrokenAndClean` |
| C-FS-13 | 前端类型诊断（0.0.42）：.ts/.tsx/.mts/.cts/.vue 走工程级 vue-tsc/tsc --noEmit（按 package.json+node_modules 定位工程、tsconfig 可在上一层）；**只展开本文件**的诊断、其它文件只计数；全干净静默通过（带检查器名）；退出异常且无输出 = Inconclusive 绝不冒充通过；无工程/无检查器/无 tsconfig = Attempted=false（自动钩子静默、手动明说） | `TestWebDiag_ProjectRunFiltersToFile` / `TestWebDiag_CleanFileOthersDirty` / `TestWebDiag_AllClean` / `TestWebDiag_InconclusiveOnSilentFailure` / `TestWebDiag_SkipsWithoutProject` / `TestWrite_AutoDiagnoseTypeScript` |
| C-FS-12 | replace 多段编辑（`edits`，与单段参数互斥）：一次调用逐段原子应用——任一段零匹配/多处未放行 → 整次失败文件零修改；每段在**前序段应用后**的内容上匹配（顺序依赖合法）；回执汇总段数 | `TestReplaceEdits_AppliesAll` / `TestReplaceEdits_AnyMissFailsAtomically` / `TestReplaceEdits_SequentialDependency` / `TestReplaceEdits_MultiMatchPolicy` / `TestReplaceEdits_Guards` |

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
| C-SEARCH-9 | 并行扫描产出**确定性**（0.0.28）：两段式——遍历只做廉价判定收集候选，固定 worker pool（NumCPU 封顶 8）并行扫内容，产出按工作区相对路径排序——结果只由文件集合决定、与调度顺序无关；刻意不按命中配额早停（半路收手会让产出集合依赖调度顺序），扫描总量由超时/取消兜底 | `TestSearch_ParallelDeterministicOutput` / `TestSearch_ParallelMultiDirCorrectness` |
| C-SEARCH-10 | 忽略目录**单一来源**（0.0.28）：`internal/platform/workspace` 定稿 9 项（.git .idea .vscode bin build dist node_modules obj vendor），大小写不敏感、只认单级目录名；search 与壳层 @ 引用（经编排层转出）共用同一份，不再各留一份硬编码 | `TestSearch_SkipsUnifiedIgnoreList` / `TestIgnoredDir_FinalList` / `TestIgnoredDir_CaseInsensitive` / `TestIgnoredDir_NoPrefixMatch` / `TestWorkspaceIgnoredDir_SingleSource` |

## C-APP：编排纪律（M2 起持续生效）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-APP-1 | 持久化/流式任何错误必须上抛到 UI 层（守卫 R2 静态强制 + 用例测试） | `TestChatService_PersistErrorPropagates` |
| C-APP-2 | 用户中断 → `EndCancelled` 终态 + 账本保留已产生事件，UI 显示"已取消" | `TestChatService_CancelKeepsEvents` |
| C-APP-3 | `Replay` 投影含 tool 卡（name/status/content）与 assistant thinking | `TestChatService_ReplayIncludesTools` |
| C-APP-6 | 项目检查命令的项目级默认（0.0.43）：AGENTS.md frontmatter `check:` 作为工作区检查命令的回退——设置显式配置优先；命令同时注入环境事实（模型知道它存在）；解析不出 = 与"未配置"完全一致（绝不猜命令） | `TestSessionFacts_AgentsCheckCommand` / `TestRunWorkspaceCheck_AgentsFallback` |
| C-APP-7 | 系统通知绑定（0.0.43）：`SendNotification` 文本有界（标题 80/正文 200 字节，UTF-8 安全）、XML 转义防注入、全局限速 3s（窗口内静默丢弃不报错）、执行失败上抛；触发判定在前端（窗口失焦才发，后端无前台会话概念）；旧内核缺方法时前端降级为无通知 | `TestSendNotification_RateLimited` / `TestSendNotification_Runs` / `TestSendNotification_ErrorPropagates` / `TestSendNotification_TextBoundaries` |
| C-APP-8 | 压缩绝不静默（UI 侧，0.0.43）：chat:context 携带 `compacted` 标记进油表读数（"历史已压缩"）；旧后端不带字段按 false 处理 | `chat.test.ts: chat:context 携带 compacted 标记进油表读数` |
| C-APP-5 | 项目规则注入（0.0.42）：工作区根 `AGENTS.md` 非空时其正文快照进本轮系统说明（上限 8KB 头尾保留并注明节选）；无/空文件整段不出现（零噪声）；每轮读一次快照、逐字稳定不破坏 prompt cache | `TestSessionFacts_AgentsRules` |
| C-APP-4 | **零块终态必须可见**：整回合没有任何增量到达就失败时（无可用渠道 / 流未建立即失败），错误必须新建一条错误气泡呈现，禁止因"没有进行中的助手气泡"而静默丢弃——否则界面表现为"消息发出去了、什么都没发生" | `chat.test.ts: 零块错误终态必须新建可见错误气泡` / `chat.test.ts: 零块空闲超时也可见` / `chat.test.ts: 零块正常终态不补空气泡` |

## C-AGT：模型上下文回放（M6）

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-AGT-1 | 第二轮 `Run` 的请求消息含上一轮 `assistant(tool_calls)` + `role=tool`，ID 配对 | `TestAgent_DerivesToolHistoryAcrossTurns` |
| C-AGT-2 | 单条 tool 结果 >4096 字节时模型侧截断并含 `truncated` 标注；账本仍是全文 | `TestAgent_TruncatesToolResultForModel` |
| C-AGT-3 | 无对应 result 的 tool_call 不进入模型消息 | `TestAgent_OmitsUnpairedToolCall` |
| C-AGT-4 | 缺 `id` 的旧事件能合成 ID 并配对，第二轮请求协议合法 | `TestAgent_SyntheticIDsForLegacyLedger` |
| C-AGT-5 | 账本存在 `EventTodo` 时，派生消息末尾含**最新一条**清单全文，且只注入一次；`DeriveInfo.LatestTodo` 同步携带该快照 | `TestDerive_InjectsLatestTodoAtEnd` |
| C-AGT-6 | 清单消息**不被任何一级折叠裁剪**（含 `BudgetTokens=1`），不得折成"已折叠/已省略"或整段丢弃 | `TestDerive_TodoSurvivesBudgetFolding` |
| C-AGT-7 | 无 `EventTodo` 的账本，派生结果与旧版逐条一致（零噪声，`LatestTodo` 为 nil） | `TestDerive_NoTodoNoInjection` |
| C-AGT-8 | 清单消息不得携带 `tool_calls`（注入不设 `lastAssistant`，否则 flush 把待配对调用并入清单） | `TestDerive_TodoMessageNeverCarriesToolCalls` |
| C-AGT-9 | fork 丢弃区间内的清单不得出现（消息与读数都不复活）；**全部 `done` 的清单仍然注入**并写明"可收尾" | `TestDerive_TodoInsideForkDropIsDiscarded` / `TestDerive_AllDoneTodoStillInjected` |
| C-AGT-10 | 自评调用不得携带工具定义（否则模型边自评边干活，步数失控） | `TestLoop_SelfAssessCarriesNoTools` |
| C-AGT-11 | 自主续跑累计段数不得超过配置额度，触顶后必问用户 | `TestLoop_AutoQuotaExhausts` / `TestLoop_AutoQuotaThenAskUser` |
| C-AGT-12 | 额度为 0（默认未开启）时步数用尽一律问用户，行为与旧版逐条等价 | `TestLoop_AutoContinueBudgetDefaultsToZero` + 既有 `TestLoop_StepLimitAskThenContinue` |
| C-AGT-13 | 清单全部 `done` 时不得续跑（结构化判定强制采纳 done，模型答 continue 也忽略）；**无清单（items 空）时自评强制降级 blocked** | `TestLoop_SelfAssessRefusesContinueWhenAllDone` / `TestLoop_SelfAssessWithoutTodoIsBlocked` |
| C-AGT-14 | 自评响应解析失败、取值非法、缺字段一律降级 `blocked`（问用户），绝不猜 `continue` | `TestParseAutoVerdict` |
| C-AGT-15 | `TodoItem.Files` 缺失（旧账本）或条目未声明 files → **不核对、不拒绝**（向后兼容降级） | `TestTodoItems_FilesOptional` / `TestUnaudited_DetectsUnwrittenFiles` |
| C-AGT-16 | 条目标 `done` 且声明 files，但本轮未观察到对其中**任一**文件的写入 → 拒绝该次 todo 提交（`IsError`），给出可执行的纠偏指引 | `TestUnaudited_DetectsUnwrittenFiles` |
| C-AGT-17 | 核账路径比对走 Windows 归一（大小写不敏感、斜杠等价、去 `./`）；空白路径与归一后重复的路径在解析期即拒绝 | `TestNormalizePathKey` / `TestTodoItems_ParsesFiles` |
| C-AGT-18 | 核账只认 `fs.write` / `fs.replace` 的**成功**结果；shell 重定向、MCP 写操作、解析不出的调用一律不认定（宁可漏判不误判）；`written` 每轮 Run 重建，只认本轮 | `TestWriteTargetOf` |
| C-AGT-19 | 历史压缩（0.0.41）：账本存在 `EventCompaction` 时，`seq < up_to_seq` 的叙事事件不投影，摘要以 `【历史摘要】` user 消息开头；截点后的轮次完整保留；`EventTodo` 是状态不是叙事，截点前的最新清单照常注入；多次压缩后一次覆盖前一次；落在 fork 丢弃区间的压缩事件不生效 | `TestDerive_CompactionReplacesOldTurns` / `TestDerive_CompactionLatestWins` |
| C-AGT-20 | 折叠到底仍超预算（声明上限）→ 先尝试 LLM 压缩（截点=保留最近 3 轮，摘要只覆盖对话叙事，工具输出仍走确定性单行）并落账 `EventCompaction`，成功则带摘要继续；摘要调用失败或压缩后仍超 → 回退旧超限错误终态；不可压缩（用户轮不足）不发起任何模型调用 | `TestAgent_OverflowTriggersCompaction` / `TestAgent_OverflowCompactionFailureFallsBack` / `TestAgent_OverflowTooFewTurnsFallsBack` |

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
| C-APP-8 | 检查自愈（0.0.34）：检查红且上一回合**正常收尾**（EndDone）→ 自动启动定向修复回合（RunSystemTurn，系统留痕 EventAssistantMsg 替代用户消息，位置引用随留痕/trailing note 给模型）；预算 2 轮封顶，检查变绿或真实用户消息发送时重置；**非正常收尾（中断/看门狗/错误）绝不触发**；会话忙时自动修复让位 | `TestAutoFix_TriggerBudgetReset` / `TestAutoFix_NonDoneNeverTriggers` / `TestAutoFix_EndToEnd`；`chat.test.ts: onAutoFix 置 running + 说明气泡` |
| C-APP-9 | 方案模式（0.0.35）：SendPlan 一轮 = **只读调研 + 实施方案**——写路径**结构性不在场**（planRegistry：shell/browser/memory/ext 不注册，fs 为白名单只读包装，write/replace 及未来新动作一律业务拒绝）；系统说明带方案段（逐字常量）；方案文本就是普通助手消息，确认后的执行是新的一手**普通回合**（方案随用户消息带走，不存在系统代执行）；终态后前端弹方案确认卡 | `TestPlanFS_Gating` / `TestSendPlan_ReadOnlyEnforced`；`chat.test.ts: 方案模式 SendPlan 一次性/确认卡取消不发`；`Composer.test.ts: 方案模式开关` |

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
| C-APR-8 | 新装默认清单 = `shell, ext_manage, mcp, browser`——四个"不确认就执行即危险"的口子（0.0.28 起）。版本迁移只做**一次性追加**：既有非空清单保留用户项与顺序、去重补入新口子；显式 `[]`（用户关掉整个审批，0.0.05 独立形态）不受影响、绝不复活；升版本号保证迁移只做一次——此后用户把某项移出清单，重启也不会被补回 | `TestChatService_ApprovalBridge` / `TestPool_ApprovalToolsMissingFieldMigratesToDefault` / `TestPool_ApprovalToolsExplicitEmptyRespected` / `TestPool_ApprovalToolsV2ListAppendsMcpBrowser` / `TestPool_ApprovalToolsV2ExplicitEmptyNotResurrected` |

## C-WF：网页正文读取（0.0.28）

> 新工具 `webfetch`。为什么补：模型此前没有任何读网页的能力（browser.snapshot 只给可交互元素、
> 每个元素文本截 60 字符），查报错方案、查库文档是硬伤。HTML 剥噪刻意 stdlib 手写
> （`readable.go` 单向状态机）——只做"剥噪声取正文"，为它引一个 HTML 解析器得不偿失。

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-WF-1 | 仅接受 http/https（含本地 dev server，不拦 localhost）；其它 scheme 与缺 host 显式拒绝 | `TestWebFetch_RejectsNonHTTPScheme` |
| C-WF-2 | HTML 按 Content-Type 优先判定（类型缺失时按 doctype/`<html` 特征兜底）：返回文档标题 + 剥除 script/style/noscript/template/svg/iframe/nav/header/footer/aside/form 后的正文文本；json/text/plain 原样透传不提取 | `TestWebFetch_ExtractsTitleAndBody` / `TestWebFetch_StripsHTMLNoise` / `TestWebFetch_PlainTextPassthrough` / `TestIsHTML_ContentTypeFirstSniffFallback` |
| C-WF-3 | 输出有界：正文超 64KB 头尾保留（与 search/fs 同配比）；响应体默认 2MB 读取上限（`max_bytes` 可配），被上限切过时附截断标注 | `TestWebFetch_DefaultBodyCapTruncates` / `TestWebFetch_MaxBytesParamTruncates` |
| C-WF-4 | 超时语义（C-TOOL-2 的 webfetch 特化）：响应未到 → `TIMEOUT` + `IsError` + `TimedOut`；读到一半超时 → 已读部分照常返回 + `\nTIMEOUT`，绝不静默丢弃已捕获内容 | `TestWebFetch_TimeoutBeforeResponse` / `TestWebFetch_TimeoutKeepsPartialBody` |
| C-WF-5 | HTTP 非 2xx → `IsError=true` 且 Content 以 `HTTP <code>` 开头并附错误页正文——报错原因常就在里面，模型可见、可推理 | `TestWebFetch_Non2xxReportsStatusAndBody` |
| C-WF-6 | 字符集策略（对齐 shelltool 解码纪律）：Content-Type 显式声明的 GBK 家族优先按 GBK 解码；未声明/声明不可处理时 UTF-8 合法原样通过，否则 GBK 兜底；仍解不开原样返回（宁可乱码可见） | `TestWebFetch_GBKDeclaredCharset` / `TestWebFetch_GBKFallbackWithoutCharset` |
| C-WF-7 | 出网走**全局 http(s) 代理**：代理来源与 gateway 同源（每次执行实时取，改配置即生效）；代理无效显式报错，绝不静默直连 | `TestWebFetch_UsesGlobalProxy` |
| C-WF-8 | 装配为**共享工具**：不依赖工作区根，纯对话也在注册表；能力边界由 `webfetch.Preface()` 常量注入系统说明（内容恒定，不破坏 prompt cache 幂等） | `TestChatService_WebFetchSharedToolAndPreface` |

## C-BR：内置浏览器与驾驶舱（0.0.28）

> browser 工具从"盲盒"升级成"驾驶舱"：改变页面状态的动作（open/scroll/click/fill/back）完成后
> 自动补一张当前视口截图，连同页面 URL 与控制台尾部随工具结果流到前端——用户实时"看见"模型
> 正在看的页面，不再只靠文字想象。截图按会话落盘到数据目录 `browser-shots/` 下，前端经壳层
> `ReadBrowserShot` 读图（防穿越）。审批：browser 可提交表单，默认**在**审批清单（C-APR-8）。

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-BR-1 | 一个会话一个 tab（独立导航历史/控制台/页面状态，并行会话互不串）；无头与有头是进程级形态、各自惰性拉起至多一个进程，open 换形态（`headless=false` 调试用）就地重建 | `TestBrowser_EndToEnd` / `TestSchema_OpenHeadlessParam` |
| C-BR-2 | open/scroll/click/fill/back 成功后自动附驾驶舱数据（`Visual` = 当前视口截图 + 落地 URL + 控制台尾部）；截图失败不毁主动作——视觉是增强不是本体，失败原因进 Content 尾注；显式 `screenshot` 动作单独要图 | `TestBrowser_EndToEnd` |
| C-BR-3 | 截图落盘 `browser-shots/<会话>/shot-NNNN`（文件名可预测、会话内单调递增不覆盖）；超约 1.5MB 等比缩到宽 ≤1280 转 JPEG；压缩失败原图保存并注明降级原因 | `TestBrowser_EndToEnd` / `TestShrinkShot` / `TestScaleImage_Dimensions` |
| C-BR-4 | 截图子目录名经 safeDirName 清洗（只留字母数字与 `-_`，分隔符与点号一律替换）——异常会话 ID 引不出截图根 | `TestSafeDirName` |
| C-BR-5 | `ReadBrowserShot` 只允许 browser-shots 内的相对路径（Clean + 前缀校验防穿越），命中返回 base64；越界/缺失显式报错，绝不用空串或占位图冒充 | `TestReadBrowserShot_RejectsTraversal` / `TestReadBrowserShot_ReturnsBase64` / `TestReadBrowserShot_MissingFileErrors` |
| C-BR-6 | 驾驶舱数据与 diff 同纪律：UI 专用、**不进模型上下文**，但随卡落账本——chat:tool 载荷带 shot/url/console，Replay 投影同构（重启后卡片不丢驾驶舱数据） | `TestDrainTurnConsumesEveryChunkKind` |
| C-BR-7 | 用户点停止 → 等待中的动作立刻被打断，返回「动作已被用户取消」（非业务失败：模型无须也无机会补救），绝不把 context canceled 伪装成"页面不存在" | `TestBrowser_EndToEnd` |
| C-BR-8 | ref 必须是**完整**非负十进制整数（要作数字下标注入 JS，严格解析是注入面的最后防线）；fill 文本经 JSON 转义嵌入页面脚本 | `TestParseRef` / `TestClickFillJS_EscapesText` |
| C-BR-9 | 视觉反馈（0.0.37）：`screenshot` 带 `for_model=true` → 截图以 data URL 随结果（`ToolResult.ModelImage`，json:"-" 绝不漏进 chat:tool）返回，agent **回合内**合成 user 消息送进模型上下文（Parts+Content 同源，仅本回合、不落账本）；IsError 结果不送图；原始字节超 1.5MB 拒绝并给出路；默认（无 for_model）不产生图像 token 成本 | `TestLoop_ModelImageReachesNextRequest` / `TestLoop_ModelImageSkippedOnError` / `TestModelImageDataURL_SizeCap` / `TestBrowser_EndToEnd`（for_model 段） |

## C-FT：右栏目录树（0.0.28）

> 右栏「目录」tab 的数据源 `ListWorkspaceDir`。壳层签名是 `(sessionID, relPath)` 而非按顶栏工作区
> 取根——树里点开的文件要走 `openFileDetail → ReadSessionFile`（会话根），两个视图必须共享同一
> 基准；树若用顶栏根，切换工作区后查看旧会话会出现"树上点开、详情 404"（0.0.11 修过的事故类别）。

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-FT-1 | 根语义与 SearchWorkspaceFiles/ReadSessionFile 同源：已落账会话用归属根，草稿回退顶栏根——树上点开的文件必须能被文件详情面板读出 | `TestBind_ListWorkspaceDir` |
| C-FT-2 | 路径校验与 fstool 同强度：绝对路径与 `../` 穿越一律拒绝；Clean 后对已存在的最深前缀做 EvalSymlinks，用真实路径做前缀判定（工作区内符号链接指向区外照样拦住——词法前缀拦不住的那一半）；只列下一层，目录在前、名称次序 | `TestBind_ListWorkspaceDir` |
| C-FT-3 | 无工作区 / 越界 / 缺失 / 非目录**显式报错**，绝不静默返回空列表装作空目录（"存在但为空"与"读不到"必须可区分） | `TestBind_ListWorkspaceDir_NoWorkspace` |

## C-BG：后台任务快照（0.0.28）

> 右栏「任务」tab 的数据源：把 shell 工具内存里的后台任务表投影给前端。任务真相只在工具实例
> 内存里、不落账本——面板是观察窗，不是事实源。数据通道是 2s 轮询而非事件推送：后台任务本无
> 推送事件（bg_status 是模型侧工具），轮询仅在面板挂载期间进行，关 tab 即停。

| ID | 契约 | 锁定测试 |
| --- | --- | --- |
| C-BG-1 | 快照按会话隔离（按 sessionID 取该会话 shell 工具的任务表）、按任务号升序稳定产出（界面列表不跳动）；会话没有工具集时返回**空表而非错误**——面板轮询不该被"没有任务"打断 | `TestChatService_BgTasksSnapshot` / `TestBind_BgTasksSnapshot` |

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
| C-INS-8 | 升级退出握手（0.0.38）：检测到应用在跑 → 先经单实例通道发 `-upgrade-exit`（空闲实例优雅退出、忙实例拒绝）→ 等待 20s 内退出则继续安装，超时中止且**绝不强制结束进程**；旧版本应用不认识该参数立即自退，安全退化为"等待/中止" | `TestEnsureAppClosed_Handshake` / `TestNoForcedAppKill` |
| C-INS-9 | 安装器**任何**失败（含"请先退出"类中止）统一 exit 1——交互模式此前只弹框不退出，自动化会把"没装成"读成"成功" | `TestEnsureAppClosed_Handshake`（errAppRunning 路径）+ 0.0.31 登记、0.0.38 修复 |

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
| 2026-10-01 | **新增 C-WF-1~8 / C-BR-1~8 / C-FT-1~3 / C-BG-1；C-SEARCH 增补 9~10；C-APR 增补 8（审批默认清单变更）** | 0.0.28 驾驶舱批次登记：① 新工具 `webfetch`（读网页正文：HTML 剥噪 stdlib 手写、GBK 兜底解码、共享工具不依赖工作区根、出网与 gateway 同源走全局代理）——模型此前没有读网页能力，查文档/查报错是硬伤。② browser 工具驾驶舱化：改变页面状态的动作自动附当前视口截图 + URL + 控制台尾部（与 diff 同纪律：UI 专用不进模型上下文、随卡落账、Replay 同构）；新增 screenshot 动作与 open 的 headless 参数；壳层 `ReadBrowserShot` 防穿越读图。③ search 两段式并行扫描（产出确定性：按路径排序、刻意不按配额早停）；`truncated, max_matches` 标注从"配额满后还有文件"收紧为"还有命中被挡"（原先对零命中文件也会误标注）。④ 忽略目录单一来源 `internal/platform/workspace`（9 项定稿，search 与壳层 @ 引用共用；归 platform 不归 core/app 的理由见包注释）。⑤ 右栏「目录」「任务」tab：`ListWorkspaceDir` 壳层签名含 sessionID（树与文件详情面板必须同一根，防"树上点开、详情 404"）；`BgTasksSnapshot` 轮询快照（无工具集返回空表而非错误）。⑥ **审批默认清单变更：`shell, ext_manage` → 追加 `mcp, browser`**——四个"不确认就执行即危险"的口子（mcp 可调宿主任意工具、browser 可提交表单）。迁移语义：channels.json v2→v3 一次性追加进既有**非空**清单（保留用户项与顺序、去重）；显式 `[]`（用户关掉整个审批的独立形态）不受影响、绝不复活；升版本号保证此后用户移出 mcp/browser 也不会被重启补回 | 0.0.28 驾驶舱批次（webfetch / browser / search / panels 四个工作流）；迁移语义锁定于 `TestPool_ApprovalToolsV2ListAppendsMcpBrowser` / `TestPool_ApprovalToolsV2ExplicitEmptyNotResurrected` |
| 2026-10-04 | **新增 C-AGT-5 ~ C-AGT-9** | 档位 1：任务清单注入模型上下文。根因是 `deriveMessagesWith` 从不投影 `EventTodo`——清单落了账本却只喂给 UI，模型每轮开局看不到计划，表现为"跨轮失忆 + 清单不遵守 + 假完成"。四条实现约束各有锁定测试：只取最新、折叠免疫、不设 `lastAssistant`（防清单吸走 tool_calls）、fork 区间不复活；全 done 也注入（模型才知道可收尾）。`DeriveInfo.LatestTodo` 读数顺带携带快照，供自主续跑的结构化判定复用（零额外 IO） | `docs/superpowers/specs/2026-10-04-task-plan-closure-design.md` 档位 1 |
| 2026-10-04 | **新增 C-AGT-10 ~ C-AGT-14** | 档位 2：自主续跑。步数用尽时先让模型对照清单自评，额度内自主续段，不再每 25 步打扰用户。额度硬封顶 3 段、默认 0 关闭、自评不带工具、无清单强制 blocked、解析失败即问用户——五条都是"决策权交给模型"的刹车。自主续跑落账复用 `EventAssistantMsg`（"（系统）已连续执行…"），前端零改动即可见、Replay 可复原 | ADR-0009 |
| 2026-10-04 | **新增 C-AGT-15 ~ C-AGT-18** | 档位 3：客观核账。否决"每项加 verify 命令、系统执行验证"方案（等于模型自己出考题自己判卷，且引入命令执行副作用），改用 `files` 声明 + 系统侧写入记录取证（`finishCall` 落账后按 `writeTargetOf` 采集，零新增 IO）。**边界**：只能证明"文件被写过"，不能证明"改对了"；模型可重交绕过——核账是纠偏不是闸门。`TodoItem` 加 `files`（可空加法，旧账本零影响） | `docs/superpowers/specs/2026-10-04-task-plan-closure-design.md` 档位 3 |
| 2026-10-05 | **新增 C-FS-8 ~ C-FS-11** | codeintel 批次：编译诊断（write/replace 落盘 `.go` 后自动 go vet 所在包——含 _test.go——错误内联回当次回执，模型同一回合自纠；超时/跳过不改写成功语义、干净静默）+ `fs.diagnose` 手动诊断 + `fs.symbols` 符号大纲（Go parser 精确、其余启发式并标注、300 条有界）。引擎取 go vet 子进程而非 gopls/x/tools：本机与用户环境普遍无 gopls（dev 机实测未装），vendor x/tools 只为诊断不值；vet 覆盖类型/语法/未定义且**含 test 文件**（go build 不查）。定义/引用跳转登记档位 2（需常驻 LSP 客户端，独立工程量不与本批混装） | `docs/superpowers/specs/2026-10-05-codeintel-diagnostics-design.md` |
| 2026-10-05 | **新增 C-APP-8 / C-FS-12** | 编程循环强化批（用户批准的"AI 干活能力"清单第 1、3 位）：① **检查自愈循环**——检查红且上一回合正常收尾时自动启动定向修复回合（`Loop.RunSystemTurn` 系统留痕开局，事件经壳层 drainTurn 与 Send 同一条桥），逻辑验证从"模型自觉"变成"循环保证"；预算 2 轮封顶、中断/错误收尾绝不触发、用户消息重置——自动修复绝不与用户抢方向盘。② **replace 多段编辑**——`edits` 多 hunk 一次调用原子应用，大文件多点修改省 N 次往返 | 本次评审结论（自愈循环/多段编辑/Plan 模式/视觉反馈/子代理五项，按序交付；本批 1、3） |
| 2026-10-06 | **新增 C-APP-9** | 方案模式（清单第 2 位）：SendPlan 一轮 = 只读调研 + 实施方案。写路径**结构性不在场**（planRegistry 重建工具集：shell/browser/memory/ext 不注册、fs 白名单只读包装），系统说明带方案段（逐字常量保 prompt cache）；方案 = 普通助手消息，确认后的执行是新的一手普通回合——审批闸门/自愈循环/审批白名单等全部既有机制在执行回合照常生效。前端：Composer"方案"开关（一次性）+ 终态后方案确认卡（执行/取消） | 用户"继续"裁决按建议顺序交付（形态按与现有交互最一致的最小闭环定案） |
| 2026-10-06 | **新增 C-BR-9** | 视觉反馈（清单第 4 位）：模型可以亲眼看自己改的页面——`screenshot` 带 `for_model=true` 时截图以 data URL 随工具结果返回，agent 回合内合成 user 消息送进模型上下文（Parts+Content 同源走三协议既有管线；仅本回合、不落账本、IsError 不送图、1.5MB 封顶）。UI 的 Visual 链路不变（Visual 给 UI、ModelImage 给模型，json:"-" 互不泄漏）；默认不带 for_model 零图像成本。UI 盲改 → 看着改 | 用户"继续"裁决按建议顺序交付（本批 4） |
