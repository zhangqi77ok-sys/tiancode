import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { createApp, nextTick, type App } from 'vue'
import { consumeEsc } from '../composables/useEsc'
import ShortcutsOverlay from './ShortcutsOverlay.vue'

let app: App | null = null
let host: HTMLElement | null = null

describe('ShortcutsOverlay 快捷键浮层', () => {
  beforeEach(() => {
    host = document.createElement('div')
    document.body.appendChild(host)
  })

  afterEach(() => {
    app?.unmount()
    app = null
    host?.remove()
    host = null
    document.body.innerHTML = ''
  })

  it('浮层列出的组合键覆盖 matchShortcut 的全部分支（逐条断言文本存在）', async () => {
    let closed = false
    app = createApp(ShortcutsOverlay, {
      onClose: () => {
        closed = true
      },
    })
    app.mount(host!)
    await nextTick()

    const content = document.body.textContent ?? ''

    // 覆盖 matchShortcut 的所有分支：
    // case 'n' -> new-session
    expect(content).toContain('Ctrl+N')
    expect(content).toContain('新建对话')

    // case 'arrowup' / 'arrowdown' -> prev/next-session
    expect(content).toContain('Ctrl+↑')
    expect(content).toContain('Ctrl+↓')
    expect(content).toContain('上一条 / 下一条会话')

    // case 'b' -> toggle-sidebar
    expect(content).toContain('Ctrl+B')
    expect(content).toContain('收起 / 展开侧栏')

    // case 'i' / '/' -> focus-input
    expect(content).toContain('Ctrl+I 或 Ctrl+/')
    expect(content).toContain('聚焦输入框')

    // 还有全局 Esc
    expect(content).toContain('Esc')

    // 按 Esc 关闭
    expect(consumeEsc()).toBe(true)
    expect(closed).toBe(true)
  })
})
