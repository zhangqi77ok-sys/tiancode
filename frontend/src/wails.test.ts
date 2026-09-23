import { afterEach, describe, expect, it } from 'vitest'
import { bridge } from './wails'

// 回归锁定：壳层是 package app 的 Bind，注入路径为 window.go.app.Bind。
// 曾误写 go.main.App，导致 w.go.main 为 undefined → 全部 IPC 抛
// "Cannot read properties of undefined (reading 'App')"（实机截图事故）。
function setGo(value: unknown) {
  ;(window as unknown as { go?: unknown }).go = value
}

const fakeApp = { Send: async () => {}, ListSessions: async () => ['s-1'] }

describe('wails bridge 绑定解析', () => {
  afterEach(() => {
    delete (window as unknown as { go?: unknown }).go
  })

  it('解析 go.app.Bind（当前壳层真实路径）', async () => {
    setGo({ app: { Bind: fakeApp } })
    await expect(bridge().app.ListSessions()).resolves.toEqual(['s-1'])
  })

  it('兼容 go.main.App（package main 绑定的情形）', async () => {
    setGo({ main: { App: fakeApp } })
    await expect(bridge().app.ListSessions()).resolves.toEqual(['s-1'])
  })

  it('鸭子类型兜底：命名变化也能解析', async () => {
    setGo({ whatever: { SomethingElse: fakeApp } })
    await expect(bridge().app.ListSessions()).resolves.toEqual(['s-1'])
  })

  it('注入 go 但解析不到绑定 → 抛可读错误（绝不静默返回空数据）', async () => {
    setGo({ unrelated: {} })
    await expect(bridge().app.ListSessions()).rejects.toThrow('未找到本地内核绑定')
  })

  it('无 go 注入（浏览器调试）→ 读操作空数据、写操作显式报错', async () => {
    await expect(bridge().app.ListSessions()).resolves.toEqual([])
    await expect(
      bridge().app.AddChannel({ id: '', name: '', protocol: '', baseUrl: '', model: '', apiKey: '' }),
    ).rejects.toThrow('浏览器调试模式')
  })
})
