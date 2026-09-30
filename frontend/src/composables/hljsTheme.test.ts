import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// 阶段 1：代码高亮配色必须跟着主题走（此前无条件引 github.css，深色下关键字与注释对比不够）。
// 两份主题 CSS 用标记串替身：测试环境（Vitest）不加载 CSS 内容，而"换的是哪一份"才是本模块的职责；
// 主题文件本身的颜色是 highlight.js 的资产，不在这里断言。
vi.mock('highlight.js/styles/github.css?inline', () => ({ default: 'LIGHT-THEME-MARKER' }))
vi.mock('highlight.js/styles/github-dark.css?inline', () => ({ default: 'DARK-THEME-MARKER' }))

const { initHljsTheme } = await import('./hljsTheme')
const { setTheme } = await import('./useTheme')

// jsdom 不实现 matchMedia：桩一个最小实现，让"跟随系统"这条路径也能测。

function stubMatchMedia(matches: boolean) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }))
}

const styleEl = () => document.getElementById('hljs-theme') as HTMLStyleElement | null

beforeEach(() => {
  stubMatchMedia(false)
  document.documentElement.removeAttribute('data-theme')
  styleEl()?.remove()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('hljs 配色跟随主题（阶段 1）', () => {
  it('深色挂 github-dark；浅色与跟随系统各自取对应那份', () => {
    initHljsTheme()
    expect(styleEl(), '挂载时就该注入一份配色').toBeTruthy()

    setTheme('dark')
    expect(styleEl()?.textContent).toBe('DARK-THEME-MARKER')

    setTheme('light')
    expect(styleEl()?.textContent).toBe('LIGHT-THEME-MARKER')

    setTheme('auto') // 系统浅色
    expect(styleEl()?.textContent).toBe('LIGHT-THEME-MARKER')

    stubMatchMedia(true) // 系统深色：跟随系统时也要跟着走
    setTheme('auto')
    expect(styleEl()?.textContent).toBe('DARK-THEME-MARKER')
  })
})
