import { describe, expect, it } from 'vitest'
import { parseAtToken, applyPick } from './atFile'

// 0.0.09：输入框 @ 文件引用的解析与替换（纯函数契约）。
describe('atFile', () => {
  it('行首 @ 触发：query 为其后已输入部分', () => {
    expect(parseAtToken('看 @foo', 6)).toEqual({ start: 2, end: 6, query: 'foo' })
    expect(parseAtToken('@', 1)).toEqual({ start: 0, end: 1, query: '' })
  })

  it('词中 @（邮箱/半词）不触发', () => {
    expect(parseAtToken('mail me a@b.com', 15)).toBeNull()
    expect(parseAtToken('abc@', 4)).toBeNull()
  })

  it('@ 与光标间有空白：不是引用', () => {
    expect(parseAtToken('@ foo', 5)).toBeNull()
  })

  it('光标不在 @ 词内：不触发', () => {
    expect(parseAtToken('@foo bar', 8)).toBeNull()
  })

  it('applyPick 替换 "@query" 为 "@<path> " 并返回新光标（0.0.11：保留 @——后端发送时解析 @路径 为附件）', () => {
    const tok = parseAtToken('看 @com', 6)!
    const r = applyPick('看 @com', 6, tok, 'frontend/src/Composer.vue')
    expect(r.text).toBe('看 @frontend/src/Composer.vue ')
    expect(r.caret).toBe('看 @frontend/src/Composer.vue '.length)
  })

  it('选中后光标继续输入：@path 后有空格不再触发新菜单', () => {
    const tok = parseAtToken('看 @com', 6)!
    const r = applyPick('看 @com', 6, tok, 'a.go')
    expect(parseAtToken(r.text, r.caret)).toBeNull() // @ 后跟空白：非引用
  })
})
