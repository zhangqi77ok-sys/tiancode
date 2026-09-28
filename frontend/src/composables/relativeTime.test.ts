import { describe, expect, it } from 'vitest'
import { relativeTime } from './relativeTime'

const NOW = 1_700_000_000_000
const MIN = 60_000
const HOUR = 3_600_000
const DAY = 86_400_000

describe('relativeTime', () => {
  it('边界映射（确定性与单位切换）', () => {
    expect(relativeTime(0, NOW)).toBe('')
    expect(relativeTime(NOW + 5, NOW)).toBe('')
    expect(relativeTime(NOW - 30_000, NOW)).toBe('刚刚')
    expect(relativeTime(NOW - 5 * MIN, NOW)).toBe('5 分钟前')
    expect(relativeTime(NOW - 3 * HOUR, NOW)).toBe('3 小时前')
    expect(relativeTime(NOW - 26 * HOUR, NOW)).toBe('昨天')
    expect(relativeTime(NOW - 5 * DAY, NOW)).toBe('5 天前')
  })

  it('超过 30 天显示具体日期', () => {
    const ms = NOW - 40 * DAY
    const d = new Date(ms)
    expect(relativeTime(ms, NOW)).toBe(`${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()}`)
  })
})
