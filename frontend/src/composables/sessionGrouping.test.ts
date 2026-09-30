import { describe, expect, it } from 'vitest'
import { buildSidebar, filterSidebar, matchesSession } from './sessionGrouping'
import type { SessionSummaryDTO } from '../wails'

function s(id: string, opts: Partial<SessionSummaryDTO> = {}): SessionSummaryDTO {
  return { id, title: '', ...opts }
}

describe('buildSidebar', () => {
  it('三分区结构：置顶 / 会话（未归属）/ 空间', () => {
    const sections = buildSidebar(
      [
        s('p', { workspace: 'D:/w/alpha', pinned: true, lastActiveMs: 100 }),
        s('u', { lastActiveMs: 200 }),
        s('a', { workspace: 'D:/w/beta', lastActiveMs: 300 }),
      ],
      'D:/w/beta',
    )
    expect(sections.map((x) => x.kind)).toEqual(['pinned', 'sessions', 'spaces'])
    expect(sections[0]).toMatchObject({ label: '置顶', items: [expect.objectContaining({ id: 'p' })] })
    expect(sections[1]).toMatchObject({ label: '会话', items: [expect.objectContaining({ id: 'u' })] })
    // 置顶会话不为其空间创建空组：alpha 无未置顶成员 → 空间组只有 beta
    expect(sections[2].groups).toHaveLength(1)
    expect(sections[2].groups[0]).toMatchObject({ label: 'beta', isCurrent: true })
    expect(sections[2].groups[0].items.map((i) => i.id)).toEqual(['a'])
  })

  it('置顶会话不重复出现在其他分区', () => {
    const sections = buildSidebar([s('p', { workspace: 'D:/w/alpha', pinned: true })], 'D:/w/alpha')
    const flat = sections.flatMap((x) => [...x.items, ...x.groups.flatMap((g) => g.items)])
    expect(flat.filter((x) => x.id === 'p')).toHaveLength(1)
  })

  it('空间分组：当前空间排最前；组内按最后活跃降序', () => {
    const sections = buildSidebar(
      [
        s('old', { workspace: 'D:/w/beta', lastActiveMs: 100 }),
        s('new', { workspace: 'D:/w/beta', lastActiveMs: 500 }),
      ],
      'D:/w/beta',
    )
    const spaces = sections.find((x) => x.kind === 'spaces')
    expect(spaces?.groups[0]).toMatchObject({ label: 'beta', isCurrent: true })
    expect(spaces?.groups[0].items.map((i) => i.id)).toEqual(['new', 'old'])
  })

  it('旧会话（无 workspace）归"会话"区；有成员的空间才出现', () => {
    const sections = buildSidebar([s('old'), s('b', { workspace: 'D:/w/beta' })], 'D:/w/beta')
    expect(sections.find((x) => x.kind === 'sessions')?.items.map((i) => i.id)).toEqual(['old'])
    expect(sections.find((x) => x.kind === 'spaces')?.groups.map((g) => g.label)).toEqual(['beta'])
  })

  it('全空：不输出分区', () => {
    expect(buildSidebar([], 'D:/w/beta')).toEqual([])
  })
})

// 本地搜索（0.0.11）：标题 / 未命名回退 ID / 工作区路径三条路都能命中；
// 过滤只做减法——空分组、空分区消失，分组规则本身不变。
describe('filterSidebar / matchesSession', () => {
  it('匹配标题与工作区路径，且不区分大小写', () => {
    expect(matchesSession(s('a', { title: '修 Bug' }), 'bug')).toBe(true)
    expect(matchesSession(s('a', { workspace: 'D:/Work/Alpha' }), 'work/alpha')).toBe(true)
    expect(matchesSession(s('a', { title: '无关' }), 'bug')).toBe(false)
    expect(matchesSession(s('a', { title: '无关' }), '')).toBe(true) // 空查询 = 不过滤
  })

  it('未命名会话按会话 ID 也能搜到', () => {
    expect(matchesSession(s('s-20260930-120000', { title: '' }), '20260930')).toBe(true)
  })

  it('过滤后空分组/空分区消失，命中分组内只留命中项', () => {
    const sections = buildSidebar(
      [
        s('p', { workspace: 'D:/w/alpha', pinned: true, title: '置顶的活' }),
        s('u', { title: '未归属的事' }),
        s('a', { workspace: 'D:/w/beta', title: '甲任务' }),
        s('b', { workspace: 'D:/w/beta', title: '乙任务' }),
      ],
      'D:/w/beta',
    )
    const filtered = filterSidebar(sections, '甲')
    // 置顶与未归属会话都不命中 → 整个分区消失，只剩命中的空间分组
    expect(filtered.map((x) => x.kind)).toEqual(['spaces'])
    expect(filtered[0].groups).toHaveLength(1)
    expect(filtered[0].groups[0].label).toBe('beta')
    expect(filtered[0].groups[0].items.map((i) => i.id)).toEqual(['a'])
  })

  it('空查询原样返回（清空过滤即恢复原行为）', () => {
    const sections = buildSidebar([s('a', { workspace: 'D:/w/beta' })], 'D:/w/beta')
    expect(filterSidebar(sections, '   ')).toBe(sections)
  })
})
