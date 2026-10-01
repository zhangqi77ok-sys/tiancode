import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { currentTheme, cycleTheme, initTheme, setTheme } from './useTheme'

// 主题三态契约（0.0.06 起亮/暗/跟随系统行为不变；实现改为"auto 由 JS 解析成实际主题、
// data-theme 始终存在"后在此锁定）：
//   · light / dark 显式写属性，系统偏好翻转不跟随；
//   · auto 按当前系统偏好解析写入，系统明暗翻转即时跟随；
//   · currentTheme 永远返回偏好模式本身（顶栏「跟随系统」标签依赖）。
const KEY = 'tiancode-theme'

let changeCbs: (() => void)[] = []
function stubMatchMedia(matches: boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches,
    media: query,
    addEventListener: (_type: string, cb: () => void) => changeCbs.push(cb),
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }))
}

// 模拟系统明暗翻转：触发已注册的 change 监听。快照后再触发——回调内部会重挂监听
// （追加进同一数组），边遍历边追加会转成死循环；真实浏览器里 change 是异步派发的，
// 不存在这种同步重入。
function flipSystem() {
  for (const cb of [...changeCbs]) cb()
}

const attr = () => document.documentElement.getAttribute('data-theme')

beforeEach(() => {
  changeCbs = []
  localStorage.removeItem(KEY)
  document.documentElement.removeAttribute('data-theme')
  stubMatchMedia(false)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('主题三态（data-theme 始终存在）', () => {
  it('light / dark 显式设置写对应属性，系统翻转不跟随', () => {
    setTheme('auto') // 先进 auto：确保系统监听已挂上
    setTheme('dark')
    expect(attr()).toBe('dark')
    stubMatchMedia(true) // 系统变深色
    flipSystem()
    expect(attr()).toBe('dark') // 显式深色不跟随
    setTheme('light')
    expect(attr()).toBe('light')
    stubMatchMedia(false)
    flipSystem()
    expect(attr()).toBe('light') // 显式浅色也不跟随
  })

  it('auto 按系统偏好解析成实际主题写入', () => {
    setTheme('auto') // 系统浅色
    expect(attr()).toBe('light')
    stubMatchMedia(true) // 系统深色
    setTheme('auto')
    expect(attr()).toBe('dark')
  })

  it('auto 下系统明暗翻转即时跟随', () => {
    setTheme('auto')
    expect(attr()).toBe('light')
    stubMatchMedia(true)
    flipSystem()
    expect(attr()).toBe('dark')
    stubMatchMedia(false)
    flipSystem()
    expect(attr()).toBe('light')
  })

  it('currentTheme 返回偏好模式本身，不被解析覆盖', () => {
    setTheme('auto')
    expect(currentTheme()).toBe('auto')
    expect(cycleTheme()).toBe('light')
    expect(cycleTheme()).toBe('dark')
    expect(cycleTheme()).toBe('auto')
  })

  it('initTheme 恢复持久化偏好', () => {
    setTheme('dark')
    document.documentElement.removeAttribute('data-theme')
    initTheme()
    expect(attr()).toBe('dark')
  })
})
