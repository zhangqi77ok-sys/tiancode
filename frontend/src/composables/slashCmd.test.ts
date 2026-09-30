import { describe, expect, it } from 'vitest'
import { applySlashPick, parseSlashToken } from './slashCmd'

// 第 7 批：/ 技能与 MCP 菜单——与 @ 引用同一层交互，选中后正文里不留命令字样。
describe('parseSlashToken', () => {
  it('行首 / 触发，query 为已输入部分', () => {
    expect(parseSlashToken('/dep', 4)).toEqual({ start: 0, end: 4, query: 'dep' })
  })

  it('空白后的 / 也触发（句中开新命令）', () => {
    expect(parseSlashToken('帮我看看 /git', 9)).toEqual({ start: 5, end: 9, query: 'git' })
  })

  it('半词与路径里的 / 不触发', () => {
    expect(parseSlashToken('2026/09', 7)).toBeNull() // 紧邻非空白
    expect(parseSlashToken('/git hub', 8)).toBeNull() // 命令词里出现空白
  })
})

describe('applySlashPick', () => {
  it('删掉 "/词"，其余文字保留', () => {
    expect(applySlashPick('帮我看看 /git', { start: 5, end: 9, query: 'git' })).toEqual({
      text: '帮我看看 ',
      caret: 5,
    })
  })
})
