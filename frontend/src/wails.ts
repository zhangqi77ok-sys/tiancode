// Wails 桥的类型化访问器。
// 为什么不用 wailsjs 生成绑定 + 全局 Window 声明：避免前端编译依赖生成步骤，
// 也规避 vue-tsc 对 declare global 增强的加载时序问题；wails 在运行时向 window
// 注入 go/runtime（与生成的绑定调用同一桥，见 https://wails.io）。

export interface ChatMessageDTO {
  role: string
  content: string
}

// 审批请求载荷（内核要"问"时推送；UI 渲染确认卡片，答复经 ResolveApproval 回流）。
// 载荷不带 sessionID：流式进行中 UI 禁止切换会话（store 保证），故卡片必然属于当前会话。
export interface ApprovalEventDTO {
  id: string
  toolName: string
  arguments: string
}

// 会话摘要：title 为空表示用户从未重命名（UI 回退显示会话 ID）
export interface SessionSummaryDTO {
  id: string
  title: string
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

interface WailsApp {
  ListSessions(): Promise<string[] | null>
  ListSessionSummaries(): Promise<SessionSummaryDTO[] | null>
  RenameSession(sessionID: string, title: string): Promise<void>
  ExportSessionMarkdown(sessionID: string): Promise<string | null>
  GetWorkspace(): Promise<string | null>
  SetWorkspace(dir: string): Promise<void>
  Replay(sessionID: string): Promise<ChatMessageDTO[] | null>
  Send(sessionID: string, text: string): Promise<void>
  Stop(sessionID: string): Promise<void>
  DeleteSession(sessionID: string): Promise<void>
  ListChannels(): Promise<ChannelListDTO | null>
  // 审批闸门（ADR-0007）：策略查询/设置 + 答复回传
  ApprovalPolicy(): Promise<string[] | null>
  SetApprovalPolicy(tools: string[]): Promise<void>
  ResolveApproval(id: string, approved: boolean, reason: string): Promise<void>
  ChannelPresets(): Promise<PresetDTO[] | null>
  AddChannel(input: ChannelInput): Promise<ChannelDTO | null>
  UpdateChannel(input: ChannelInput): Promise<void>
  DeleteChannel(id: string): Promise<void>
  SetActiveChannel(id: string): Promise<void>
  DiscoverModels(input: ChannelInput): Promise<string[] | null>
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
        ExportSessionMarkdown: async () => '',
        GetWorkspace: async () => '',
        SetWorkspace: offlineWrite,
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
