import { describe, expect, it } from 'vitest'
import { workspaceLabel } from './workspaceLabel'

// 0.0.07：顶栏标签 = 这场对话正在用的路。核心场景：
// 已落账会话归属 A、下一场根 B → 主标签 A、副标签 B（禁止都叫"当前"）。
describe('workspaceLabel', () => {
  it('已落账会话（归属 A）+ 下一场根 B：主 A 副 B', () => {
    const l = workspaceLabel({ isDraft: false, sessionWorkspace: 'D:/proj/a', draftWorkspace: 'D:/proj/b' })
    expect(l.main).toBe('proj/a')
    expect(l.mainTitle).toContain('这场对话正在使用')
    expect(l.mainTitle).toContain('D:/proj/a')
    expect(l.sub).toContain('新建对话将使用')
    expect(l.sub).toContain('proj/b')
    expect(l.subTitle).toBe('D:/proj/b')
  })

  it('归属与下一场根相同：不出副标签', () => {
    const l = workspaceLabel({ isDraft: false, sessionWorkspace: 'D:/proj/a', draftWorkspace: 'D:/proj/a' })
    expect(l.main).toBe('proj/a')
    expect(l.sub).toBe('')
  })

  it('已落账纯对话：主标签「这场对话没有工作区」', () => {
    const l = workspaceLabel({ isDraft: false, sessionWorkspace: '', draftWorkspace: '' })
    expect(l.main).toBe('这场对话没有工作区')
  })

  it('已落账纯对话 + 下一场有根：副标签显示下一场根', () => {
    const l = workspaceLabel({ isDraft: false, sessionWorkspace: '', draftWorkspace: 'D:/proj/b' })
    expect(l.main).toBe('这场对话没有工作区')
    expect(l.sub).toContain('proj/b')
  })

  it('草稿：主标签 = 下一场的根（它就是发出后的归属）', () => {
    const l = workspaceLabel({ isDraft: true, sessionWorkspace: '', draftWorkspace: 'D:/proj/b' })
    expect(l.main).toBe('proj/b')
    expect(l.mainTitle).toContain('发出后将使用')
  })

  it('草稿无工作区：主标签「这场对话没有工作区」', () => {
    const l = workspaceLabel({ isDraft: true, sessionWorkspace: '', draftWorkspace: '' })
    expect(l.main).toBe('这场对话没有工作区')
  })

  it('短标签至少上一级+当前名', () => {
    const l = workspaceLabel({ isDraft: false, sessionWorkspace: 'D:/deep/nest/proj/dist', draftWorkspace: '' })
    expect(l.main).toBe('proj/dist')
  })
})
