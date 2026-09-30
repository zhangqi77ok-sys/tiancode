import { describe, expect, it } from 'vitest'
import {
  WINDOW_CHUNK,
  WINDOW_INITIAL_TAIL,
  WINDOW_MAX,
  growFrom,
  initialFrom,
  shouldGrow,
  trimFrom,
} from './messageWindow'

// 阶段 2：窗口规则是纯函数，边界必须锁死——这是"长会话只挂视口附近的回合"
// 与"贴底/发送/切换落最新"两条要求能同时成立的依据。

describe('messageWindow（回合窗口）', () => {
  it('初次进入只挂尾部，历史多长都不一次性铺开', () => {
    expect(initialFrom(0)).toBe(0)
    expect(initialFrom(10)).toBe(0) // 少于一个窗口：全挂
    expect(initialFrom(WINDOW_INITIAL_TAIL)).toBe(0)
    expect(initialFrom(500)).toBe(500 - WINDOW_INITIAL_TAIL)
  })

  it('向上增挂按块走，到 0 就停', () => {
    expect(growFrom(16, 200)).toBe(0)
    expect(growFrom(120, 200)).toBe(120 - WINDOW_CHUNK)
    expect(growFrom(0, 200)).toBe(0)
  })

  it('贴近挂载区顶部才增挂；已经在顶部（from=0）不再增挂', () => {
    expect(shouldGrow(120, 10)).toBe(true)
    expect(shouldGrow(120, 5000)).toBe(false)
    expect(shouldGrow(0, 10)).toBe(false)
  })

  it('只在贴底时裁掉最老的一块；不贴底一律不动', () => {
    const from = initialFrom(500) // 460
    expect(trimFrom(from, 500, false)).toBe(from) // 用户在看历史：不许抽走内容
    expect(trimFrom(from, 500, true)).toBe(from) // 挂载数没超上限：不裁
    // 挂满超过上限（用户一路向上看过）：贴底时从老的一侧按块裁
    expect(trimFrom(0, 500, true)).toBe(WINDOW_CHUNK)
  })

  it('裁剪不会把挂载数裁到上限以下（停在上限处，不做多余动作）', () => {
    const total = 500
    let from = 0
    for (let i = 0; i < 50; i++) from = trimFrom(from, total, true)
    expect(total - from).toBeLessThanOrEqual(WINDOW_MAX)
    expect(total - from).toBeGreaterThan(WINDOW_MAX - WINDOW_CHUNK)
  })

  it('消息很少时既不增挂也不裁剪（边界不折腾）', () => {
    expect(initialFrom(30)).toBe(0)
    expect(growFrom(0, 30)).toBe(0)
    expect(trimFrom(0, 30, true)).toBe(0)
  })
})
