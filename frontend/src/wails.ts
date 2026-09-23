// Wails 桥的类型化访问器。
// 为什么不用 wailsjs 生成绑定 + 全局 Window 声明：避免前端编译依赖生成步骤，
// 也规避 vue-tsc 对 declare global 增强的加载时序问题；wails 在运行时向 window
// 注入 go/runtime（与生成的绑定调用同一桥，见 https://wails.io）。

export interface ChatMessageDTO {
  role: string
  content: string
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

interface WailsApp {
  ListSessions(): Promise<string[] | null>
  Replay(sessionID: string): Promise<ChatMessageDTO[] | null>
  Send(sessionID: string, text: string): Promise<void>
  Stop(sessionID: string): Promise<void>
  DeleteSession(sessionID: string): Promise<void>
  ListChannels(): Promise<ChannelListDTO | null>
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

// bridge 返回类型化的 wails 注入对象。
// 纯浏览器（vite dev）没有注入：返回桩实现，便于视觉调试与设计迭代
// ——读操作桩返回空数据，绝不伪造业务数据。
export function bridge(): WailsBridge {
  const w = window as unknown as {
    go?: { main: { App: WailsApp } }
    runtime?: WailsRuntime
  }
  if (!w.go || !w.runtime) {
    const stub: WailsBridge = {
      app: {
        ListSessions: async () => [],
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
  return { app: w.go.main.App, runtime: w.runtime }
}
