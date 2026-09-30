import { beforeEach, describe, expect, it } from 'vitest'
import { loadSidebarCollapsed, matchShortcut, saveSidebarCollapsed } from './shortcuts'

// 第 8 批：Ctrl+B 收起/展开侧栏——必须与既有组合键互不误伤（尤其不能触发新建会话）。
describe('matchShortcut', () => {
  it('Ctrl/Cmd+B → 切换侧栏（不是新建会话）', () => {
    expect(matchShortcut({ key: 'b', ctrlKey: true })).toBe('toggle-sidebar')
    expect(matchShortcut({ key: 'B', metaKey: true })).toBe('toggle-sidebar')
    expect(matchShortcut({ key: 'b', ctrlKey: true })).not.toBe('new-session')
  })

  it('既有组合键不受影响', () => {
    expect(matchShortcut({ key: 'n', ctrlKey: true })).toBe('new-session')
    expect(matchShortcut({ key: 'i', ctrlKey: true })).toBe('focus-input')
    expect(matchShortcut({ key: '/', metaKey: true })).toBe('focus-input')
    expect(matchShortcut({ key: 'ArrowUp', ctrlKey: true })).toBe('prev-session')
    expect(matchShortcut({ key: 'ArrowDown', ctrlKey: true })).toBe('next-session')
  })

  it('无修饰键与带 Shift/Alt 的组合都不抢', () => {
    expect(matchShortcut({ key: 'b' })).toBeNull()
    expect(matchShortcut({ key: 'b', ctrlKey: true, shiftKey: true })).toBeNull()
    expect(matchShortcut({ key: 'b', ctrlKey: true, altKey: true })).toBeNull()
    expect(matchShortcut({ key: 'z', ctrlKey: true })).toBeNull()
  })
})

describe('侧栏折叠态的持久化', () => {
  beforeEach(() => localStorage.clear())

  it('写入后读回一致', () => {
    expect(loadSidebarCollapsed()).toBe(false) // 缺省展开
    saveSidebarCollapsed(true)
    expect(loadSidebarCollapsed()).toBe(true)
    saveSidebarCollapsed(false)
    expect(loadSidebarCollapsed()).toBe(false)
  })
})
