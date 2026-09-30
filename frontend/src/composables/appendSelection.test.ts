import { describe, expect, it } from 'vitest'
import { appendToDraft } from './appendSelection'

// 第 7 批：选中文字「放进输入框」——只追加，不替换草稿，不自动发送。
describe('appendToDraft', () => {
  it('空草稿：直接放进选区', () => {
    expect(appendToDraft('', '  func main() {}  ')).toBe('func main() {}')
  })

  it('已有草稿：空一行再追加（不覆盖原内容）', () => {
    expect(appendToDraft('先看这个：', 'foo.go:12')).toBe('先看这个：\n\nfoo.go:12')
  })

  it('草稿尾部空白先收掉，不堆空行', () => {
    expect(appendToDraft('第一段\n\n', '第二段')).toBe('第一段\n\n第二段')
  })

  it('空选区：草稿原样返回', () => {
    expect(appendToDraft('草稿', '   ')).toBe('草稿')
    expect(appendToDraft('', '')).toBe('')
  })
})
