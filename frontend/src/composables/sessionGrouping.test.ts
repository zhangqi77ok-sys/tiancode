import { describe, expect, it } from 'vitest'
import { buildSidebar } from './sessionGrouping'
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
      ['p', 'u', 'a', 'new'],
      'D:/w/beta',
    )
    expect(sections.map((x) => x.kind)).toEqual(['pinned', 'sessions', 'spaces'])
    expect(sections[0]).toMatchObject({ label: '置顶', items: [expect.objectContaining({ id: 'p' })] })
    expect(sections[1]).toMatchObject({ label: '会话', items: [expect.objectContaining({ id: 'u' })] })
    // 置顶会话不为其空间创建空组：alpha 无未置顶成员 → 空间组只有 beta（含新建未发送会话）
    expect(sections[2].groups).toHaveLength(1)
    expect(sections[2].groups[0]).toMatchObject({ label: 'beta', isCurrent: true })
    expect(sections[2].groups[0].items.map((i) => i.id)).toEqual(['new', 'a'])
  })

  it('置顶会话不重复出现在其他分区', () => {
    const sections = buildSidebar([s('p', { workspace: 'D:/w/alpha', pinned: true })], ['p'], 'D:/w/alpha')
    const flat = sections.flatMap((x) => [...x.items, ...x.groups.flatMap((g) => g.items)])
    expect(flat.filter((x) => x.id === 'p')).toHaveLength(1)
  })

  it('空间分组：当前空间排最前；组内按最后活跃降序', () => {
    const sections = buildSidebar(
      [
        s('old', { workspace: 'D:/w/beta', lastActiveMs: 100 }),
        s('new', { workspace: 'D:/w/beta', lastActiveMs: 500 }),
      ],
      ['old', 'new'],
      'D:/w/beta',
    )
    const spaces = sections.find((x) => x.kind === 'spaces')
    expect(spaces?.groups[0]).toMatchObject({ label: 'beta', isCurrent: true })
    expect(spaces?.groups[0].items.map((i) => i.id)).toEqual(['new', 'old'])
  })

  it('新建未发送会话：有当前空间归其最上，未设置工作区归"会话"区', () => {
    const withWs = buildSidebar([], ['fresh'], 'D:/w/beta')
    expect(withWs[0].kind).toBe('spaces')
    expect(withWs[0].groups[0].items[0]).toMatchObject({ id: 'fresh' })

    const noWs = buildSidebar([], ['fresh'], '')
    expect(noWs[0].kind).toBe('sessions')
    expect(noWs[0].items[0]).toMatchObject({ id: 'fresh' })
  })

  it('旧会话（无 workspace）归"会话"区；有成员的空间才出现', () => {
    const sections = buildSidebar([s('old'), s('b', { workspace: 'D:/w/beta' })], ['old', 'b'], 'D:/w/beta')
    expect(sections.find((x) => x.kind === 'sessions')?.items.map((i) => i.id)).toEqual(['old'])
    expect(sections.find((x) => x.kind === 'spaces')?.groups.map((g) => g.label)).toEqual(['beta'])
  })

  it('全空（无摘要无新建）：不输出分区', () => {
    expect(buildSidebar([], [], 'D:/w/beta')).toEqual([])
  })
})
