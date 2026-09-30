// 全局快捷键映射（第 8 批）：抽成纯函数——"哪个组合键触发哪件事"是行为契约，
// 该被单测直接锁住，而不是散在 App.vue 的 if 链里（新增一个键最容易误伤另一个）。
//
// 已占用：Ctrl/Cmd+N 新建对话、Ctrl/Cmd+I 与 Ctrl/Cmd+/ 聚焦输入框、
// Ctrl/Cmd+↑/↓ 切换会话、Esc 中断/关浮层、**Ctrl/Cmd+B 收起或展开侧栏**（第 8 批）。

export type Shortcut = 'new-session' | 'focus-input' | 'prev-session' | 'next-session' | 'toggle-sidebar' | null

export interface KeyLike {
  key: string
  ctrlKey?: boolean
  metaKey?: boolean
  shiftKey?: boolean
  altKey?: boolean
}

export function matchShortcut(e: KeyLike): Shortcut {
  const mod = !!(e.ctrlKey || e.metaKey)
  if (!mod) return null
  const k = e.key.toLowerCase()
  // Shift/Alt 组合不抢（用户可能在用别的工具/输入法的组合键）
  if (e.shiftKey || e.altKey) return null
  switch (k) {
    case 'n':
      return 'new-session'
    case 'i':
    case '/':
      return 'focus-input'
    case 'arrowup':
      return 'prev-session'
    case 'arrowdown':
      return 'next-session'
    case 'b':
      return 'toggle-sidebar'
    default:
      return null
  }
}

// 侧栏折叠态记在 localStorage（跨重启保留）；读写都容错——存储不可用只是不记忆，
// 绝不让一个纯展示偏好把界面搞崩。
const SIDEBAR_KEY = 'tiancode.sidebar.collapsed'

export function loadSidebarCollapsed(): boolean {
  try {
    return localStorage.getItem(SIDEBAR_KEY) === '1'
  } catch {
    return false
  }
}

export function saveSidebarCollapsed(v: boolean): void {
  try {
    localStorage.setItem(SIDEBAR_KEY, v ? '1' : '0')
  } catch {
    // 存不下就算了：折叠态是会话内的展示偏好
  }
}
