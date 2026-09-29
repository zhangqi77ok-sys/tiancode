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
  // 问答卡（role='ask'）：问题与选项来自 tool_call 参数，Content 为用户答复
  question?: string
  options?: string[]
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

export interface ChannelListDTO {
  channels: ChannelDTO[]
  activeId: string
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
  // 原生目录选择框：返回选中目录，取消返回空串（工作区由用户在对话框里选，而非手敲路径）
  PickWorkspace(): Promise<string>
  // kind：mcp | skill | skill-dir。取消返回空串。skill-dir 返回 {"files":[{name,body}]}
  PickImport(kind: string): Promise<string>
  PinSession(sessionID: string, pinned: boolean): Promise<void>
  Replay(sessionID: string): Promise<ChatMessageDTO[] | null>
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

// winClose 关闭应用（v2 的关闭入口是 Quit）。
export function winClose(): void {
  const rt = windowRuntime()
  ;(rt.Quit ?? rt.WindowClose)?.()
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
        Replay: async () => [],
        Send: async () => {},
        Stop: async () => {},
        DeleteSession: offlineWrite,
        ListChannels: async () => ({ channels: [], activeId: '' }),
        ChannelPresets: async () => [],
        AddChannel: offlineWrite,
        UpdateChannel: offlineWrite,
        DeleteChannel: offlineWrite,
        SetActiveChannel: offlineWrite,
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
