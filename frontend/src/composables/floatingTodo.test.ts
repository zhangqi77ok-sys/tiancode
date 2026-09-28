import { describe, expect, it } from 'vitest'
import {
  DEFAULT_POS,
  MIN_VISIBLE,
  clampPos,
  isDrag,
  latestTodos,
  parsePos,
} from './floatingTodo'
import type { ChatMsg } from '../stores/chat'

function msg(partial: Partial<ChatMsg> & { role: ChatMsg['role'] }): ChatMsg {
  return { content: '', ...partial }
}

describe('floatingTodo', () => {
  // 实机最可能踩的坏情况：拖动把面板推出可视区 → 用户找不回来
  it('位置钳制在容器内，且至少保留 MIN_VISIBLE 可见', () => {
    const bounds = { w: 800, h: 600 }
    const self = { w: 268, h: 300 }

    expect(clampPos({ x: 400, y: 300 }, self, bounds)).toEqual({ x: 400, y: 300 })
    // 右下越界：左/上边最多到 容器 - MIN_VISIBLE
    expect(clampPos({ x: 9999, y: 9999 }, self, bounds)).toEqual({
      x: 800 - MIN_VISIBLE,
      y: 600 - MIN_VISIBLE,
    })
    // 左上越界：不能为负
    expect(clampPos({ x: -50, y: -20 }, self, bounds)).toEqual({ x: 0, y: 0 })
  })

  // 元素比容器还大时，上界仍按 MIN_VISIBLE 算（保留可见区），且不得出现负上界
  it('元素大于容器时仍按 MIN_VISIBLE 留出可见区，且上界不为负', () => {
    expect(clampPos({ x: 999, y: 999 }, { w: 900, h: 900 }, { w: 300, h: 200 })).toEqual({
      x: 300 - MIN_VISIBLE,
      y: 200 - MIN_VISIBLE,
    })
    // 元素比 MIN_VISIBLE 还小（收起态图标）时要求整体留在容器内：上界 = 容器 - 元素
    expect(clampPos({ x: 50, y: 50 }, { w: 20, h: 20 }, { w: 40, h: 40 })).toEqual({ x: 20, y: 20 })
  })

  it('解析持久化位置：有效值可用，缺失/损坏/非有限数回退 null', () => {
    expect(parsePos('{"x":12,"y":34}')).toEqual({ x: 12, y: 34 })
    expect(parsePos(null)).toBeNull()
    expect(parsePos('')).toBeNull()
    expect(parsePos('{')).toBeNull()
    expect(parsePos('{"x":12}')).toBeNull()
    expect(parsePos('{"x":"12","y":34}')).toBeNull()
    expect(parsePos('{"x":null,"y":34}')).toBeNull()
  })

  // 会话切换/重放后必须仍取到"当前会话最新的那份清单"（同卡原地更新）
  it('任务快照取最后一条 todo 卡（原地更新的最新快照）', () => {
    const msgs = [
      msg({ role: 'user', content: 'u' }),
      msg({ role: 'todo', id: 'm-2', todos: [{ text: 'a', status: 'pending' }] }),
      msg({ role: 'assistant', content: '规划中' }),
      msg({ role: 'todo', id: 'm-2', todos: [{ text: 'a', status: 'done' }] }),
    ]
    expect(latestTodos(msgs)).toEqual({ id: 'm-2', items: [{ text: 'a', status: 'done' }] })
  })

  it('无任务卡时返回 null（悬浮件据此不渲染）', () => {
    expect(latestTodos([])).toBeNull()
    expect(latestTodos([msg({ role: 'user', content: 'u' })])).toBeNull()
  })

  // 点击展开不能被手抖吞掉：位移超过阈值才算拖动
  it('拖动判定有阈值', () => {
    expect(isDrag(1, 1)).toBe(false)
    expect(isDrag(4, 4)).toBe(false)
    expect(isDrag(5, 0)).toBe(true)
    expect(isDrag(0, -9)).toBe(true)
  })

  it('默认位置在容器左上区域（贴近原内联卡位置）', () => {
    expect(DEFAULT_POS.x).toBeGreaterThanOrEqual(0)
    expect(DEFAULT_POS.y).toBeGreaterThan(0)
  })
})
