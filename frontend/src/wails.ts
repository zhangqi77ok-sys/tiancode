// Wails 桥的类型化访问器。
// 为什么不用 wailsjs 生成绑定 + 全局 Window 声明：避免前端编译依赖生成步骤，
// 也规避 vue-tsc 对 declare global 增强的加载时序问题；wails 在运行时向 window
// 注入 go/runtime（与生成的绑定调用同一桥，见 https://wails.io）。

export interface ChatMessageDTO {
  role: string
  content: string
  toolName?: string
  status?: string
  thinking?: string
  // 工具卡语义标签与结构化 diff（0.2.13 起账本携带；旧账本缺省 → UI 回退工具名）
  title?: string
  op?: string
  diff?: string
  // 撤销元数据（0.0.07）：hasUndo 决定是否显示"恢复写入前"；恢复走后端
  callId?: string
  hasUndo?: boolean
  undoPath?: string
  undoNote?: string
  // 附件（0.0.10）：用户消息的图片/文件（重放后仍显示；图片带 DataURL）
  attachments?: { kind: string; name: string; mediaType?: string; dataUrl?: string; path?: string; inline?: string }[]
  // 问答卡（role='ask'）：问题与选项来自 tool_call 参数，Content 为用户答复
  question?: string
  options?: string[]
  // 驾驶舱数据（0.0.28，browser 工具，历史恢复与实时事件同构）：截图相对路径
  // （browser-shots 根下正斜杠，经壳层 ReadBrowserShot 读图）+ 落地 URL + 控制台尾部。
  // 旧账本缺省为空/不带字段；非 browser 工具不带这些字段。
  shot?: string
  url?: string
  console?: string[]
  // 账本事件序号（第 6 批）：用户消息带它——「从这条消息重跑」的分叉锚点
  seq?: number
}

// chat:tool 事件载荷（实时流与 store.onTool 共用一份类型，避免两处手写漂移）。
// 字段语义与 ChatMessageDTO 同名对齐；running 卡不带驾驶舱字段。
export interface ChatToolEventDTO {
  sessionID: string
  name: string
  status: string
  summary: string
  content?: string
  diff?: string
  title?: string
  op?: string
  callID?: string
  hasUndo?: boolean
  undoPath?: string
  undoNote?: string
  // 驾驶舱数据（0.0.28）：仅 browser 工具的终态卡携带；shot 相对 browser-shots 根
  shot?: string
  url?: string
  console?: string[]
}

// 审批请求载荷：内核要"问"时推送（UI 渲染确认卡片，答复经 ResolveApproval 回流）。
// sessionID 必填（0.2.25 多会话）：卡片按它归位到发起它的会话——缺标识的载荷被
// store 丢弃并报错（宁可丢卡也不插进当前视图＝串会话）。

// 会话摘要：title 为空表示用户从未重命名（UI 回退显示会话 ID）；
// workspace 为空表示 0.2.6 前的旧账本（前端归入"未分组"）；
// lastActiveMs 为 0 表示全新会话未发生轮次（前端不显示时间）
export interface SessionSummaryDTO {
  id: string
  title: string
  workspace?: string
  pinned?: boolean
  lastActiveMs?: number
  // 0.0.19 用量沉淀（账本 usage 事件累计；旧账本/未发生轮次缺省 0）
  promptTokens?: number
  completionTokens?: number
  totalTokens?: number
  // 0.0.24 近 7 天（按 usage 事件的 at 时间戳；旧事件无时间戳不计入）
  prompt7d?: number
  completion7d?: number
  total7d?: number
}

// 渠道视图（与 app.ChannelDTO 一一对应；密钥不出现在此，只有 hasKey）
export interface ChannelDTO {
  id: string
  name: string
  protocol: string
  baseUrl: string
  model: string
  hasKey: boolean
  active: boolean
  // 多协议渠道层（0.2.17）：池模型能力面
  models?: string[]
  priority?: number
  weight?: number
  /** enabled | manually_disabled | auto_disabled */
  status?: string
  // 高级字段（0.2.19）：auto_ban 与网关改写；此前绑定层丢字段，UI 设置从未生效
  autoBan?: boolean
  modelMapping?: Record<string, string>
  paramOverride?: Record<string, unknown>
  headerOverride?: Record<string, string>
  // 渠道级鉴权（0.2.20）：缺省 = 协议默认（openai → Bearer；anthropic → x-api-key）
  auth?: AuthDTO
  // 上下文上限（token；0/缺省 = 未配置）：本地派生历史时分级折叠用，不发给上游
  contextLimit?: number
  // 每百万 token 单价（0.0.24 成本估算；0/缺省 = 未配置，不显示金额）
  priceIn?: number
  priceOut?: number
  // 凭证摘要（列表卡片"N 条 · M 禁用"；逐条管理走 ListCredentials）
  credentialCount?: number
  credentialDisabled?: number
}

// 渠道级鉴权配置：type = default|bearer|header|query|none；
// name 为请求头名/URL 参数名；value 支持 {api_key} 占位符（空 = 仅凭证）
export interface AuthDTO {
  type: string
  name?: string
  value?: string
}

// 单条凭证管理视图（脱敏预览；明文永不出现在前端）
export interface CredentialDTO {
  index: number
  preview: string
  enabled: boolean
}

// 渠道连通性测试结果（走真实链路；失败无副作用）
export interface TestResultDTO {
  ok: boolean
  ms: number
  model: string
  reply?: string
  error?: string
}

// ---- Codex（ChatGPT 订阅）账号授权（0.2.21）----

// 授权启动结果：authUrl 为 auth.openai.com 原始链接；desktopUrl 为 chatgpt.com 包装
//（默认打开它）；listening=true 时本机 1455 回环已就绪（浏览器授权后自动完成）
export interface CodexStartDTO {
  sessionId: string
  authUrl: string
  desktopUrl: string
  listening: boolean
}

// 授权状态：pending（等待）| done（凭证就绪待绑定）| error
export interface CodexStatusDTO {
  state: string
  error?: string
  display?: string
}

// 绑定结果（凭证不回显：只有脱敏摘要）
export interface CodexBindResultDTO {
  channelId: string
  name: string
  display: string
  created: boolean
}

// 渠道的 ChatGPT 账号绑定摘要（渠道管理回显）
export interface CodexCredentialDTO {
  bound: boolean
  display?: string
  expiresAt?: number
}

// 代理出口探测结果（IP 与地区：确认节点是否符合上游要求）
export interface ProxyInfoDTO {
  ip: string
  country: string
}

// 「应用到文件」的写入回执（ProposeFileWrite 返回）：写入已完成（改了就是改了），
// 界面据它出一张结果卡（路径 + diff + 新建/覆盖）。
export interface ProposeWriteResultDTO {
  path: string
  isNew: boolean
  diff: string
  bytes: number
}

// 撤回本轮的结果（第 6 批）：restored = 已恢复；skipped = 撤不回（含原因）。
export interface RevertResultDTO {
  round: number
  restored: string[]
  skipped: string[]
}

// 工作区检查命令的一处位置引用（第 8 批）：界面点它 = 与搜索结果同一打开入口。
export interface CheckRefDTO {
  path: string
  line: number
  col: number
  text: string
}

// 一次检查的结果（skipped = 命令为空或已有一次在跑，什么都没发生）。
export interface CheckResultDTO {
  sessionID: string
  command: string
  output: string
  refs: CheckRefDTO[]
  failed: boolean
  timedOut: boolean
  skipped: boolean
  at: number
}

// 工作区可选项（第 8 批）：openAtLine empty = 用系统默认程序打开；
// checkCommand 空 = 任何时候都不跑检查（绝不猜 go test）。
// shellTimeoutSeconds（0.0.25）：这个工作区里 shell 前台命令的默认超时（0 = 内置 120s，
// 上限 600s；模型仍可用 timeout_seconds 逐条覆盖）。
export interface WorkspaceSettingsDTO {
  openAtLine: string
  checkCommand: string
  // shell 审批白名单（0.0.24）：命令以这些前缀开头时免审批确认
  shellAllow?: string[]
  shellTimeoutSeconds?: number
}

// 语气设置（第 8 批）：内置 50 条由后端给（id / 名称 / 做法），前端不复制名单。
export interface ToneEntryDTO {
  id: string
  name: string
  practice: string
}

// 语气面板数据：mode 只可能 fixed / auto；default 不能出现在 disabled 里
// （后端保存时拒绝，界面照实显示那条错误）。
export interface TonesViewDTO {
  mode: string
  default: string
  disabled: string[]
  builtin: ToneEntryDTO[]
}

// 文件只读正文（第 8 批：文件详情面板）。limit 是 fs 的单次读取上限（字节），
// truncated=true 表示只给了前 limit 字节——界面必须照实说明，不能装作读全了。
export interface FileBodyDTO {
  path: string
  content: string
  truncated: boolean
  limit: number
}

// 目录树一层条目（右栏「目录」tab）：modTime 为 Unix 毫秒（后端不掺展示格式）。
export interface DirEntryDTO {
  name: string
  isDir: boolean
  modTime: number
}

// 本会话后台任务快照（右栏「任务」tab）：log 为头尾保留的有界输出（已解码 UTF-8）。
// startedAt 为 Unix 毫秒；exitCode 运行中为 -1。
export interface BgTaskDTO {
  id: string
  command: string
  pid: number
  running: boolean
  exitCode: number
  startedAt: number
  log: string
}

// 用户点链接 → 会话浏览器打开（0.0.29 驾驶舱）的返回载荷：与 chat:tool 的驾驶舱
// 字段同源同义（shot 为 browser-shots 相对路径）；output/title 供前端合成与模型
// 工具卡同构的本地卡（面板数据源唯一：会话缓冲派生）。
export interface BrowserViewDTO {
  shot: string
  url: string
  console: string[]
  output: string
  title: string
}

// 从这条用户消息重跑的结果（第 6 批）：text = 原消息原文（前端据此重发）。
export interface RerunResultDTO {
  text: string
  reverted: string[]
  skipped: string[]
}

export interface ChannelListDTO {
  channels: ChannelDTO[]
  activeId: string
}

// 分页投影（0.3 尾屏优先）：messages 为切片，total 是投影总条数，from 是切片
// 起点的下标（0 = 前面没有更早的了）。
export interface ReplayPageDTO {
  messages: ChatMessageDTO[]
  total: number
  from: number
}

// 时间线一轮（0.0.20）：锚点 = 用户消息（userSeq 供「回滚到此轮之前」定位），
// files 是该轮检查点里的文件（revertable = 该检查点还没被回滚消费）。
export interface RoundInfoDTO {
  round: number
  userSeq: number
  text: string
  files: { path: string; revertable: boolean }[]
}

// 两级记忆原文（0.0.21 记忆管理面板）：每行一条，行号即后端 Delete 的 1 基锚点。
// project 为空 = 纯对话（没有项目记忆，不是错误）。
export interface MemoryViewDTO {
  global: string[]
  project: string[]
}

// 一条跨会话搜索命中（0.0.23）：anchorSeq 是所属轮的用户消息 seq——点击复用
// 时间线跳转链路（selectSession + jumpToSeq）定位到那一轮。
export interface SearchHitDTO {
  sessionID: string
  sessionTitle: string
  workspace?: string
  lastActiveMs: number
  role: 'user' | 'assistant'
  anchorSeq: number
  snippet: string
  truncated?: boolean
}

export interface SearchSessionsResultDTO {
  hits: SearchHitDTO[]
  query: string
}

// 单渠道健康读数（0.0.23）：successRate = -1 表示"近 N 天无请求"（读数未知，
// 界面写"暂无数据"而不是编 100%）；avgLatencyMs 只按成功样本算建流耗时。
export interface ChannelHealthDTO {
  channelID: string
  ok: number
  fail: number
  faults: number
  credFaults: number
  successRate: number
  avgLatencyMs: number
  lastError?: string
  lastErrorAt?: number
}

// 导入预检结果（0.0.23）：valid=false 时 reason 是给用户看的原因。
export interface BackupPreviewDTO {
  path: string
  valid: boolean
  format: number
  version: string
  exportedAt: number
  fileCount: number
  sessionCount: number
  totalBytes: number
  reason: string
}

// 恢复结果（0.0.23）：written/skipped 如实回传，界面不编"全部恢复"。
export interface BackupApplyResultDTO {
  file: string
  written: number
  skipped: number
}

// 更新检查结果（0.0.20；GitHub Releases 为源，dev 构建恒无更新）
export interface UpdateInfoDTO {
  current: string
  latest: string
  hasUpdate: boolean
  pageUrl: string
  assetName: string
  assetUrl: string
  assetSize: number
}

// 用户命令行（0.3）的执行回执：output 为（有界）输出；isError 对应非零退出/超时。
export interface UserShellResultDTO {
  output: string
  isError: boolean
}

export interface PresetDTO {
  key: string
  name: string
  protocol: string
  baseUrl: string
  suggestedModel: string
}

// 新增/更新入参；更新时 apiKey 留空 = 保持原密钥（后端契约，见 app.ChannelInput）
export interface ChannelInput {
  id: string
  name: string
  protocol: string
  baseUrl: string
  model: string
  apiKey: string
  // 多协议渠道层（0.2.17）：多模型/优先级/权重/启停；缺省走池默认（models=[model]、priority=100）
  models?: string[]
  priority?: number
  weight?: number
  status?: string
  // 高级字段（0.2.19）：随表单落盘（此前绑定层丢字段——契约断层）
  autoBan?: boolean
  modelMapping?: Record<string, string>
  paramOverride?: Record<string, unknown>
  headerOverride?: Record<string, string>
  // 渠道级鉴权（0.2.20）：undefined/type=default = 协议默认
  auth?: AuthDTO
  // 上下文上限（token；0 = 不限）。估算口径：4 个 ASCII 字符 ≈ 1 token、
  // 1 个非 ASCII 字符 ≈ 1 token（保守上界）——见后端 derive.go。
  contextLimit?: number
  priceIn?: number
  priceOut?: number
}

// Git 面板（0.0.24）：一行变更（porcelain 状态码 + 路径；?? = 未跟踪）
export interface GitStatusEntryDTO {
  path: string
  x: string
  y: string
  untracked: boolean
}

// createAppStub 生成"调用即明确报错"的桩。
// 为什么不用静默桩：绑定路径错时静默返回空数据，UI 会显示"0 个会话/无渠道"，
// 让人误以为数据丢了；必须让错误说清原因（这是本轮真实踩到的坑）。
function failingApp(reason: string): WailsApp {
  const boom = async (): Promise<never> => {
    throw new Error(reason)
  }
  return new Proxy({} as WailsApp, {
    get: () => boom,
  })
}

// resolveApp 解析 Wails 注入的绑定对象。
// Wails v2 按 `window.go.<包名>.<结构体名>` 注入：我们的壳层是 package app 的 Bind，
// 因此是 go.app.Bind（曾误写 go.main.App，导致全部 IPC 抛 "reading 'App'"）。
// 先按已知候选路径取，再按"具备 Send/ListSessions 方法"鸭子类型兜底，避免再被命名变更绊倒。
function resolveApp(go: unknown): WailsApp | null {
  if (!go || typeof go !== 'object') return null
  const ns = go as Record<string, Record<string, unknown>>
  for (const pkg of ['app', 'main']) {
    const bag = ns[pkg]
    if (!bag || typeof bag !== 'object') continue
    for (const name of ['Bind', 'App']) {
      const candidate = bag[name]
      if (candidate && typeof (candidate as WailsApp).Send === 'function') {
        return candidate as WailsApp
      }
    }
  }
  // 兜底：任何命名空间下具备完整方法集的对象
  for (const bag of Object.values(ns)) {
    if (!bag || typeof bag !== 'object') continue
    for (const candidate of Object.values(bag)) {
      const c = candidate as WailsApp
      if (c && typeof c.Send === 'function' && typeof c.ListSessions === 'function') {
        return c
      }
    }
  }
  return null
}

export interface McpServerDTO {
  id: string
  name: string
  transport: 'stdio' | 'http'
  command: string
  args: string
  env: string
  url: string
  headers: string
  enabled: boolean
}

export interface SkillItemDTO {
  id: string
  name: string
  description: string
  body: string
  enabled: boolean
}

interface WailsApp {
  ListSessions(): Promise<string[] | null>
  ListSessionSummaries(): Promise<SessionSummaryDTO[] | null>
  RenameSession(sessionID: string, title: string): Promise<void>
  ExportSessionMarkdown(sessionID: string): Promise<string | null>
  GetWorkspace(): Promise<string | null>
  GetExtensions(): Promise<{ mcp: McpServerDTO[]; skills: SkillItemDTO[] } | null>
  SaveExtensions(file: { mcp: McpServerDTO[]; skills: SkillItemDTO[] }): Promise<void>
  SetWorkspace(dir: string): Promise<void>
  // 会话迁移（0.0.19）：把会话归属迁到另一个空间；空 dir = 移出空间（纯对话归属）。
  // 展示（侧栏分组/顶栏标签）与工具根同一规则，下一轮起文件操作落新目录。
  MoveSession(sessionID: string, dir: string): Promise<void>
  // 任务栏闪烁（0.0.19）：后台会话结束时调用，让"跑完了"穿透当前焦点被看见。
  FlashWindow(): Promise<void>
  // 真正退出应用（0.0.21）：仅关窗确认框确认后调用——后台有轮次在跑时，
  // 关窗语义（0.0.24）：X / Alt+F4 = 隐藏到托盘（后台轮次继续跑）；真退出只在托盘菜单。
  HideToTray(): Promise<void>
  // 记忆管理（0.0.21）：模型能记的用户必须看得见、删得掉。
  MemoryLines(sessionID: string): Promise<MemoryViewDTO | null>
  MemoryDelete(sessionID: string, scope: 'global' | 'workspace', line: number): Promise<void>
  MemoryClear(sessionID: string, scope: 'global' | 'workspace'): Promise<void>
  // 跨会话搜索（0.0.23）：在所有账本里搜（只读、有界）；workspace 非空 = 限定归属。
  SearchSessions(query: string, workspace: string, limit: number): Promise<SearchSessionsResultDTO | null>
  // 渠道健康读数（0.0.23）：近 N 天按渠道的成功率/建流耗时/最近错误。
  ChannelHealth(days: number): Promise<Record<string, ChannelHealthDTO> | null>
  // 备份与恢复（0.0.23）：弹系统对话框选路径；ApplyBackup 返回写入/跳过条数。
  ExportBackupTo(version: string): Promise<string>
  PickBackupFile(): Promise<string>
  PreviewBackup(path: string): Promise<BackupPreviewDTO | null>
  ApplyBackup(path: string, overwrite: boolean): Promise<BackupApplyResultDTO>
  // 时间线（0.0.20）：历轮一览 + 按任意轮回滚（保留对话历史）。
  RoundTimeline(sessionID: string): Promise<RoundInfoDTO[] | null>
  RevertToRound(sessionID: string, userSeq: number): Promise<RevertResultDTO | null>
  // 自更新（0.0.20）：检查 GitHub 最新 release；确认后下载安装包并拉起安装器（应用自退重启）。
  CheckUpdate(): Promise<UpdateInfoDTO | null>
  ApplyUpdate(): Promise<void>
  // Git 面板（0.0.24）：这场对话工作区的变更清单与单文件 diff。
  GitStatusFiles(sessionID: string): Promise<GitStatusEntryDTO[] | null>
  GitFileDiff(sessionID: string, path: string): Promise<string | null>
  // 原生目录选择框：返回选中目录，取消返回空串（工作区由用户在对话框里选，而非手敲路径）
  PickWorkspace(): Promise<string>
  // 顶栏分支（0.0.11）：已落账会话取该会话自己的工作区，草稿取"下一场新对话"的根；
  // 没有工作区 / 不是 git 仓库 / 命令失败 → 空串（界面不显示，绝不编造分支名）。
  CurrentBranch(sessionID: string): Promise<string | null>
  // 在资源管理器中显示（0.0.06）：工具卡"打开文件所在目录"。
  // 0.0.11：相对路径按**这场对话**的工作区根解析（此前用"下一场新对话"的根，
  // 切换工作区后旧会话会指到别的目录）；无工作区/越界/不存在显式报错。
  RevealInExplorer(sessionID: string, path: string): Promise<void>
  // 用系统默认程序打开（0.0.11）：路径解析同 Reveal；目录直接打开目录本身。
  OpenInDefaultApp(sessionID: string, path: string): Promise<void>
  // 打开文件并（配置了「在这一行打开」时）定位到第 line 行（第 8 批）。
  // 未配置 / 行号 0 / 命令起不来 → 与 OpenInDefaultApp 完全一致的退回路径。
  OpenAtLine(sessionID: string, path: string, line: number): Promise<void>
  // 工作区可选项（第 8 批）：在这条对话的工作区上读写 workspace-settings.json。
  WorkspaceSettings(sessionID: string): Promise<WorkspaceSettingsDTO>
  SaveWorkspaceSettings(sessionID: string, ws: WorkspaceSettingsDTO): Promise<void>
  // 读取这场对话工作区内某文件的正文（第 8 批：文件详情面板只读浏览）。
  // 超限只给前半（truncated + limit 说明上限）；越界/缺失/二进制显式报错。
  ReadSessionFile(sessionID: string, path: string): Promise<FileBodyDTO>
  // 读取 browser 工具的会话截图（0.0.28 驾驶舱）：relPath 传 chat:tool 的 shot 字段
  // 原样（browser-shots 根下正斜杠相对路径）；返回图片字节 base64（前端拼 data URL）。
  // 越界/缺失显式报错（服务端 Clean+前缀校验防穿越）。
  ReadBrowserShot(relPath: string): Promise<string>
  // 用户点对话里的网址 → 会话浏览器打开（0.0.29）：与模型共用同一 tab（所见即所控），
  // 返回驾驶舱载荷（见 BrowserViewDTO）。仅 http/https；失败显式报错——调用方
  // 据此回退系统浏览器（openExternal），绝不让对话窗口本身导航走。
  BrowserNavigate(sessionID: string, url: string): Promise<BrowserViewDTO>
  // 目录树一层列表（右栏「目录」tab）：relPath 相对这场对话的工作区（空串 = 根），
  // 目录在前、名称次序；越界/缺失/无工作区显式报错（fstool 同款校验，含符号链接解析）。
  ListWorkspaceDir(sessionID: string, relPath: string): Promise<DirEntryDTO[] | null>
  // 本会话 shell bg_start 后台任务快照（右栏「任务」tab 轮询拉取）。
  // 会话没有 shell 工具集 = 空表（不报错）；任务真相在后端内存，不落账本。
  BgTasksSnapshot(sessionID: string): Promise<BgTaskDTO[] | null>
  // 语气设置（第 8 批）：读取当前设置与内置 50 条（侧栏底部「语气」入口用）。
  GetTones(): Promise<TonesViewDTO>
  // 保存语气设置。非法值（如停用默认语气）报错并原样显示；下一轮对话生效。
  SaveTones(file: { mode: string; default: string; disabled: string[] }): Promise<void>
  // 列出已启用 MCP 服务器公布的工具名（第 8 批：/ 菜单里当场选工具，不花一轮 tool=list）。
  // 只读探测：不写账本、不产生工具卡。远程服务器返回现成的「不支持自动列工具」错误原文。
  ProbeMcpServer(name: string): Promise<string[]>
  // 系统保存对话框写文本文件（0.0.06：导出会话"另存为文件"）。取消返回空串。
  SaveTextFile(defaultName: string, content: string): Promise<string>
  // 打开内部日志目录（0.0.09）：排障入口——轮次/上游请求/看门狗事件按天落盘
  OpenLogDir(): Promise<void>
  // 恢复一次 write/replace 写入前的内容（0.0.07）。失败（文件被改过等）显式报错。
  RestoreToolWrite(sessionID: string, callID: string): Promise<string>
  // 撤回最近一个未撤回的轮次（第 6 批）：按轮次检查点恢复该轮改过的文件。
  // skipped 里是撤不回的文件（超限 / 本轮之后被改过），绝不静默。
  RevertRound(sessionID: string): Promise<RevertResultDTO | null>
  // 从这条用户消息重跑（第 6 批）：撤回其后文件改动 + 账本分叉（旧行不改写），
  // 返回原文供重新发送；skipped 同上。
  RerunFrom(sessionID: string, userSeq: number): Promise<RerunResultDTO | null>
  // @ 文件引用（0.0.09/0.0.10 会话化）：列出**这场对话**工作区的文件。
  SearchWorkspaceFiles(sessionID: string, query: string): Promise<string[] | null>
  // 带附件发送（0.0.10）：图片/文件 JSON 数组；失败上抛（前端保留待发送区）。
  // forceTool（第 7 批）：本轮"模型开口前必须先调用"的工具 JSON（{"name","arguments"}；
  // 空串 = 不强制）——用户在输入框里指定了技能或 MCP 工具时才有值。
  SendWithAttachments(sessionID: string, text: string, attachments: string, forceTool: string): Promise<void>
  // 代码块「应用到文件」：内容直接写入工作区，返回写入回执（路径 + diff + 新建/覆盖）。
  ProposeFileWrite(sessionID: string, path: string, content: string): Promise<ProposeWriteResultDTO | null>
  // kind：mcp | skill | skill-dir。取消返回空串。skill-dir 返回 {"files":[{name,body}]}
  PickImport(kind: string): Promise<string>
  PinSession(sessionID: string, pinned: boolean): Promise<void>
  Replay(sessionID: string): Promise<ChatMessageDTO[] | null>
  // 分页投影（0.3 尾屏优先）：切会话先取最后一屏，向上滚动再补更早的。
  // from 是"从末尾往前数已载入的位置"（ReplayOlder 取 [from-limit, from)）。
  ReplayTail(sessionID: string, limit: number): Promise<ReplayPageDTO | null>
  ReplayOlder(sessionID: string, from: number, limit: number): Promise<ReplayPageDTO | null>
  // 用户命令行（0.3）：复用会话 shell 工具（同一超时）与审批闸门（闸门含 shell 时
  // 同样弹审批卡）。拒绝时 isError=true、output 为拒绝说明。
  RunUserCommand(sessionID: string, command: string): Promise<UserShellResultDTO | null>
  // 提交说明（0.3）：读工作区 diff 让当前模型生成一条提交说明（不落账本、不写盘）。
  SuggestCommitMessage(sessionID: string): Promise<string | null>
  // git add -A + commit（0.3）：前端确认框放行后才调用；仅此两条 git 改写命令，
  // push/reset/clean 等路径在实现里根本不存在。
  GitStageAndCommit(sessionID: string, message: string): Promise<string | null>
  Send(sessionID: string, text: string): Promise<void>
  Stop(sessionID: string): Promise<void>
  DeleteSession(sessionID: string): Promise<void>
  ListChannels(): Promise<ChannelListDTO | null>
  // 审批闸门（ADR-0007）：策略查询/设置 + 答复回传
  ApprovalPolicy(): Promise<string[] | null>
  SetApprovalPolicy(tools: string[]): Promise<void>
  ResolveApproval(id: string, approved: boolean, reason: string): Promise<void>
  // 问答交互（ask_user）：答案原样回流给模型继续推理
  ResolveAsk(id: string, answer: string): Promise<void>
  ChannelPresets(): Promise<PresetDTO[] | null>
  AddChannel(input: ChannelInput): Promise<ChannelDTO | null>
  UpdateChannel(input: ChannelInput): Promise<void>
  DeleteChannel(id: string): Promise<void>
  SetActiveChannel(id: string): Promise<void>
  SetActiveModel(id: string, model: string): Promise<void>
  DiscoverModels(input: ChannelInput): Promise<string[] | null>
  // 渠道测试与凭证管理（0.2.19）
  TestChannel(id: string): Promise<TestResultDTO | null>
  ListCredentials(id: string): Promise<CredentialDTO[] | null>
  SetCredentialEnabled(id: string, index: number, enabled: boolean): Promise<void>
  // Codex（ChatGPT 订阅）账号授权（0.2.21）
  StartCodexOAuth(): Promise<CodexStartDTO | null>
  PollCodexOAuth(sessionID: string): Promise<CodexStatusDTO | null>
  BindCodexOAuth(
    sessionID: string,
    channelID: string,
    name: string,
    codeOrURL: string,
  ): Promise<CodexBindResultDTO | null>
  ImportCodexCredential(channelID: string, name: string, raw: string): Promise<CodexBindResultDTO | null>
  CodexCredentialOf(channelID: string): Promise<CodexCredentialDTO | null>
  // 全局上游代理（0.2.22）：ChatGPT 授权与对话共用同一出口
  GetProxy(): Promise<string | null>
  SetProxy(proxy: string): Promise<void>
  CheckProxy(proxy: string): Promise<ProxyInfoDTO | null>
}

interface WailsRuntime {
  // 泛型单载荷：wails 事件桥每次回调携带一个 payload（chat:chunk/terminal 均如此）
  EventsOn<T = unknown>(name: string, callback: (data: T) => void): void
}

export interface WailsBridge {
  app: WailsApp
  runtime: WailsRuntime
}

// 浏览器调试模式下的写操作桩：显式报错而非假装成功——
// "看着保存成功其实没保存"比直接报错更贵（与后端"未实现协议显式拒绝"同一纪律）。
const offlineWrite = async (): Promise<never> => {
  throw new Error('浏览器调试模式：未连接本地内核，无法修改配置')
}

// ---- 无边框窗口控制（自绘标题栏）----
// Wails v2 运行时注入 window.runtime。注意两点（0.2.16 实机事故）：
//   - 方法名是英式拼写 WindowMinimise/WindowToggleMaximise（美式拼写不存在 → 静默无效果）；
//   - v2 没有 WindowClose，关闭应用用 Quit()。
// 浏览器调试模式没有 runtime——?. 静默降级（无用户可行动的报错）。
interface WailsWindowRuntime {
  WindowMinimise?: () => void
  WindowMinimize?: () => void // 兼容旧/别名写法
  WindowToggleMaximise?: () => void
  WindowHide?: () => void // 0.0.24：dev 浏览器里 winClose 的等效动作
  Quit?: () => void
  WindowClose?: () => void // 兼容别名
  BrowserOpenURL?: (url: string) => void
}

function windowRuntime(): WailsWindowRuntime {
  return ((window as unknown as { runtime?: WailsWindowRuntime }).runtime ?? {}) as WailsWindowRuntime
}

// openExternal 用系统默认浏览器打开链接（Codex 账号授权跳转）。
// 浏览器调试模式没有 Wails runtime：降级 window.open（开发可用，不静默失败）。
export function openExternal(url: string): void {
  const rt = windowRuntime()
  if (rt.BrowserOpenURL) {
    rt.BrowserOpenURL(url)
    return
  }
  window.open(url, '_blank')
}

// winMinimize 最小化窗口。
export function winMinimize(): void {
  const rt = windowRuntime()
  ;(rt.WindowMinimise ?? rt.WindowMinimize)?.()
}

// winToggleMaximize 最大化/还原窗口。
export function winToggleMaximize(): void {
  windowRuntime().WindowToggleMaximise?.()
}

// winClose（0.0.24 语义变更）：X = 隐藏到托盘（后台轮次继续跑），
// 真正退出只在托盘菜单——走后端 OnBeforeClose 的统一拦截（quitting 才放行）。
export function winClose(): void {
  const w = window as unknown as { go?: { app?: { HideToTray?: () => Promise<void> } } }
  if (w.go?.app?.HideToTray) {
    void w.go.app.HideToTray()
    return
  }
  // 纯浏览器 dev：没有托盘概念，直接隐藏当前窗口等效于"离开"
  windowRuntime().WindowHide?.()
}

// bridge 返回类型化的 wails 注入对象。三种情形：
//  1. 未注入 go（纯浏览器 vite dev）→ 读操作空数据、写操作显式报错的调试桩；
//  2. 注入了 go 但解析不到绑定（路径/版本不匹配）→ 全部调用抛可读原因，绝不静默返回空数据；
//  3. 正常 → 返回真实绑定。
export function bridge(): WailsBridge {
  const w = window as unknown as {
    go?: Record<string, unknown>
    runtime?: WailsRuntime
  }
  if (!w.go) {
    const stub: WailsBridge = {
      app: {
        ListSessions: async () => [],
        ListSessionSummaries: async () => [],
        RenameSession: offlineWrite,
        ApprovalPolicy: async () => [],
        SetApprovalPolicy: offlineWrite,
        ResolveApproval: offlineWrite,
        ResolveAsk: offlineWrite,
        ExportSessionMarkdown: async () => '',
        GetWorkspace: async () => '',
        GetExtensions: async () => ({ mcp: [], skills: [] }),
        SaveExtensions: offlineWrite,
        SetWorkspace: offlineWrite,
        PickWorkspace: offlineWrite,
        PickImport: offlineWrite,
        PinSession: offlineWrite,
        MoveSession: offlineWrite,
        FlashWindow: async () => {},
        HideToTray: async () => {},
        MemoryLines: async () => ({ global: [], project: [] }),
        MemoryDelete: offlineWrite,
        MemoryClear: offlineWrite,
        SearchSessions: async () => ({ hits: [], query: '' }),
        ChannelHealth: async () => ({}),
        ExportBackupTo: async () => '',
        PickBackupFile: async () => '',
        PreviewBackup: async () => ({
          path: '',
          valid: false,
          format: 0,
          version: '',
          exportedAt: 0,
          fileCount: 0,
          sessionCount: 0,
          totalBytes: 0,
          reason: '离线模式：备份不可用',
        }),
        ApplyBackup: async () => ({ file: '-', written: 0, skipped: 0 }),
        RoundTimeline: async () => [],
        RevertToRound: offlineWrite,
        CheckUpdate: async () => ({
          current: 'dev',
          latest: '',
          hasUpdate: false,
          pageUrl: '',
          assetName: '',
          assetUrl: '',
          assetSize: 0,
        }),
        ApplyUpdate: offlineWrite,
        GitStatusFiles: async () => [],
        GitFileDiff: async () => '',
        Replay: async () => [],
        ReplayTail: async () => ({ messages: [], total: 0, from: 0 }),
        ReplayOlder: async () => ({ messages: [], total: 0, from: 0 }),
        RunUserCommand: offlineWrite,
        SuggestCommitMessage: offlineWrite,
        GitStageAndCommit: offlineWrite,
        Send: async () => {},
        Stop: async () => {},
        DeleteSession: offlineWrite,
        ListChannels: async () => ({ channels: [], activeId: '' }),
        ChannelPresets: async () => [],
        AddChannel: offlineWrite,
        UpdateChannel: offlineWrite,
        DeleteChannel: offlineWrite,
        SetActiveChannel: offlineWrite,
        SetActiveModel: offlineWrite,
        CurrentBranch: async () => '',
        ReadSessionFile: async () => ({ path: '', content: '', truncated: false, limit: 0 }),
        ReadBrowserShot: async () => '',
        BrowserNavigate: offlineWrite,
        ListWorkspaceDir: async () => [],
        BgTasksSnapshot: async () => [],
        OpenAtLine: offlineWrite,
        WorkspaceSettings: async () => ({ openAtLine: '', checkCommand: '', shellTimeoutSeconds: 0 }),
        SaveWorkspaceSettings: offlineWrite,
        ProbeMcpServer: async () => [],
        GetTones: async () => ({ mode: 'fixed', default: 'plain', disabled: [], builtin: [] }),
        SaveTones: offlineWrite,
        RevealInExplorer: offlineWrite,
        OpenInDefaultApp: offlineWrite,
        SaveTextFile: async () => '',
        OpenLogDir: offlineWrite,
        RestoreToolWrite: async () => {
          throw new Error('离线模式：恢复不可用')
        },
        SearchWorkspaceFiles: async () => [],
        SendWithAttachments: offlineWrite,
        ProposeFileWrite: offlineWrite,
        RevertRound: offlineWrite,
        RerunFrom: offlineWrite,
        DiscoverModels: offlineWrite,
        TestChannel: offlineWrite,
        ListCredentials: async () => [],
        SetCredentialEnabled: offlineWrite,
        StartCodexOAuth: offlineWrite,
        PollCodexOAuth: async () => ({ state: 'pending' }),
        BindCodexOAuth: offlineWrite,
        ImportCodexCredential: offlineWrite,
        CodexCredentialOf: async () => ({ bound: false }),
        GetProxy: async () => '',
        SetProxy: offlineWrite,
        CheckProxy: offlineWrite,
      },
      runtime: { EventsOn: () => {} },
    }
    return stub
  }

  const app = resolveApp(w.go)
  if (!app) {
    return {
      app: failingApp('未找到本地内核绑定（期望 window.go.app.Bind）。请确认安装包与前端构建版本一致，或重装应用。'),
      runtime: w.runtime ?? { EventsOn: () => {} },
    }
  }
  return { app, runtime: w.runtime ?? { EventsOn: () => {} } }
}
