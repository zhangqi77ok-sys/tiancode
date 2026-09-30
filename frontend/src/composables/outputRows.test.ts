import { describe, expect, it } from 'vitest'
import { parseInlineRefs, parseOutputRows, parseSearchRows } from './outputRows'

// 第 8 批：工具卡输出的行解析——哪一行能点、点到哪个文件的哪一行，由这里锁死。
describe('parseInlineRefs（shell 等非 diff 输出）', () => {
  it('foo.go:120: undefined → 路径 foo.go、行号 120', () => {
    const rows = parseInlineRefs('foo.go:120: undefined')!
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({ prefix: 'foo.go', line: 120, lineNo: '120', openable: true })
  })

  it('path:line:col 也认，行号列显示两者', () => {
    const rows = parseInlineRefs('internal/app/x.go:7:3: syntax error')!
    expect(rows[0]).toMatchObject({ prefix: 'internal/app/x.go', line: 7, lineNo: '7:3', openable: true })
  })

  it('普通句子不变成链接', () => {
    expect(parseInlineRefs('PASS\nok  \ttiancode/internal/app\t0.9s')).toBeNull()
    expect(parseInlineRefs('note:12:3 说的是注释里的编号')).toBeNull() // 前缀不像路径
  })

  it('缩进的报错行同样可点，行与行互不影响', () => {
    const rows = parseInlineRefs('    a/b.go:9: boom\nplain')!
    expect(rows[0]).toMatchObject({ prefix: 'a/b.go', line: 9, openable: true })
    expect(rows[1].openable).toBe(false)
  })
})

describe('parseSearchRows（search 输出）', () => {
  it('命中行可点、上下文行不可点', () => {
    const rows = parseSearchRows('src/a.go-2-func f() {\nsrc/a.go:3:NEEDLE\nsrc/a.go-4-}')!
    expect(rows[0]).toMatchObject({ prefix: 'src/a.go', line: 2, openable: false })
    expect(rows[1]).toMatchObject({ prefix: 'src/a.go', line: 3, openable: true })
  })

  it('files_only 列表整行可点，页脚不算路径', () => {
    const rows = parseSearchRows('a/b.go\n(truncated, max_matches 3)\nno matches')!
    expect(rows[0]).toMatchObject({ prefix: 'a/b.go', openable: true })
    expect(rows[1].openable).toBe(false)
    expect(rows[2].openable).toBe(false)
  })
})

describe('parseOutputRows（总入口）', () => {
  it('search 卡失败时不解析（保持原外观）', () => {
    expect(parseOutputRows('src/a.go:3:x', { isSearch: true, searchOK: false })).toBeNull()
  })

  it('空内容返回 null', () => {
    expect(parseOutputRows('   ', { isSearch: false, searchOK: true })).toBeNull()
  })
})
