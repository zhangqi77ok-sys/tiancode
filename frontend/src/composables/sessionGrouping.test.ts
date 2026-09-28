import { describe, expect, it } from 'vitest'
import { groupSessions } from './sessionGrouping'
import type { SessionSummaryDTO } from '../wails'

function s(id: string, workspace?: string, title = ''): SessionSummaryDTO {
  return { id, title, workspace }
}

describe('groupSessions', () => {
  it('按空间分组：当前空间排最前', () => {
    const groups = groupSessions(
      [s('a', 'D:/work/alpha'), s('b', 'D:/work/beta')],
      ['a', 'b'],
      'D:/work/beta',
    )
    expect(groups).toHaveLength(2)
    expect(groups[0]).toMatchObject({ label: 'beta', isCurrent: true })
    expect(groups[0].items.map((i) => i.id)).toEqual(['b'])
    expect(groups[1]).toMatchObject({ label: 'alpha', isCurrent: false })
  })

  it('分组标签取路径末段', () => {
    const groups = groupSessions([s('a', 'D:/work/alpha')], ['a'], 'D:/work/alpha')
    expect(groups[0].label).toBe('alpha')
  })

  it('旧会话无工作区归入"未分组"，不消失', () => {
    const groups = groupSessions([s('old')], ['old'], 'D:/work/beta')
    const unset = groups.find((g) => !g.isCurrent)
    expect(unset?.label).toBe('未分组')
    expect(unset?.items.map((i) => i.id)).toEqual(['old'])
  })

  it('新建未发送的会话（无摘要）归入当前空间最上方', () => {
    const groups = groupSessions([s('a', 'D:/work/alpha')], ['new-1', 'a'], 'D:/work/alpha')
    const cur = groups.find((g) => g.isCurrent)
    expect(cur?.items[0]).toMatchObject({ id: 'new-1' })
    expect(cur?.items[1]).toMatchObject({ id: 'a' })
  })

  it('未设置工作区时"未分组"即当前空间（合并，不重复建组）', () => {
    const groups = groupSessions([s('old')], ['old'], '')
    expect(groups).toHaveLength(1)
    expect(groups[0].label).toBe('未分组')
    expect(groups[0].isCurrent).toBe(true)
  })
})
