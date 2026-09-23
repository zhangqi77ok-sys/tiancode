// Wails 桥的类型化访问器。
// 为什么不用 wailsjs 生成绑定 + 全局 Window 声明：避免前端编译依赖生成步骤，
// 也规避 vue-tsc 对 declare global 增强的加载时序问题；wails 在运行时向 window
// 注入 go/runtime（与生成的绑定调用同一桥，见 https://wails.io）。

export interface ChatMessageDTO {
  role: string
  content: string
}

interface WailsApp {
  ListSessions(): Promise<string[] | null>
  Replay(sessionID: string): Promise<ChatMessageDTO[] | null>
  Send(sessionID: string, text: string): Promise<void>
  Stop(sessionID: string): Promise<void>
}

interface WailsRuntime {
  // 泛型单载荷：wails 事件桥每次回调携带一个 payload（chat:chunk/terminal 均如此）
  EventsOn<T = unknown>(name: string, callback: (data: T) => void): void
}

export interface WailsBridge {
  app: WailsApp
  runtime: WailsRuntime
}

// bridge 返回类型化的 wails 注入对象。
// 纯浏览器（vite dev）没有注入：返回桩实现，便于视觉调试与设计迭代
// ——桩只返回空数据，绝不伪造业务数据（避免"看着能跑其实没接线"的假象）。
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
      },
      runtime: { EventsOn: () => {} },
    }
    return stub
  }
  return { app: w.go.main.App, runtime: w.runtime }
}
