// 工具卡输出的行解析（第 8 批）：抽成纯函数——"哪一行能点、点到哪"是行为契约，
// 该被单测直接锁住，而不是埋在组件的模板里。
//
// 三种来源共用一套行模型：
//   1. search 命中行 `path:line:text`（可点）与上下文行 `path-line-text`（不可点）；
//   2. search 的 files_only 路径列表（整行即路径）；
//   3. 其它非 diff 输出（go test / go build 报错行）里的 `path:line(:col):`（可点）。

export interface OutputRow {
  key: number
  prefix: string // 路径（文件列表模式即整行）
  sep: string // ':' 命中 / '-' 上下文 / '' 纯路径
  lineNo: string
  line: number // 引用行号（0 = 不知道，只打开文件）
  text: string
  hit: boolean
  openable: boolean
}

const RESULT_HIT = /^(.*?):(\d+):(.*)$/
const RESULT_CTX = /^(.*?)-(\d+)-(.*)$/
// 行内引用：`path:line:` 或 `path:line:col:`（编译器/测试输出最常见的那一形态）。
// 路径部分不许含空白与冒号；再要求它"像路径"（含 / . \），免得把「note:12:3」
// 这类普通说明文字变成链接。
const INLINE_REF = /^\s*([^\s:]+?):(\d+)(?::(\d+))?:\s?(.*)$/

// pathLike 只认带分隔符的前缀：foo.go / a/b.go / a\b.go 算，note 不算。
export function pathLike(p: string): boolean {
  return /[./\\]/.test(p)
}

function plainRow(key: number, text: string): OutputRow {
  return { key, prefix: '', sep: '', lineNo: '', line: 0, text, hit: false, openable: false }
}

// parseSearchRows 解析 search 输出：有命中行时按 命中/上下文 渲染；
// 没有命中行（files_only 路径列表、no matches、页脚）时按路径列表渲染。
export function parseSearchRows(content: string): OutputRow[] | null {
  const raw = content.split('\n')
  if (raw.some((l) => RESULT_HIT.test(l))) {
    return raw.map((l, i) => {
      const hit = RESULT_HIT.exec(l)
      if (hit) {
        return {
          key: i,
          prefix: hit[1],
          sep: ':',
          lineNo: hit[2],
          line: Number(hit[2]),
          text: hit[3],
          hit: true,
          openable: true,
        }
      }
      const ctx = RESULT_CTX.exec(l)
      // 上下文行：路径照显示但不可点（"-数字-" 在带连字符的路径上可能误判，宁可不点）
      if (ctx) {
        return {
          key: i,
          prefix: ctx[1],
          sep: '-',
          lineNo: ctx[2],
          line: Number(ctx[2]),
          text: ctx[3],
          hit: false,
          openable: false,
        }
      }
      return plainRow(i, l)
    })
  }
  // files_only：整段是路径列表。页脚/说明行（"(" 开头或含空格，如
  // "(truncated, max_matches 50)"）不当作路径，避免做出假可点。
  return raw.map((l, i) => {
    const p = l.trim()
    const isPath = !!p && !p.startsWith('(') && !/\s/.test(p)
    if (!isPath) return plainRow(i, l)
    return { key: i, prefix: p, sep: '', lineNo: '', line: 0, text: '', hit: false, openable: true }
  })
}

// parseInlineRefs 解析"其它非 diff 输出"：逐行找 `path:line(:col):`。
// 一行都没找到时返回 null——调用方保持原来的整块 pre 外观，
// 不为"可能可点"改变普通输出的样子。
export function parseInlineRefs(content: string): OutputRow[] | null {
  const rows = content.split('\n').map((l, i) => {
    const ref = INLINE_REF.exec(l)
    if (ref && pathLike(ref[1])) {
      return {
        key: i,
        prefix: ref[1],
        sep: ':',
        lineNo: ref[3] ? `${ref[2]}:${ref[3]}` : ref[2],
        line: Number(ref[2]),
        text: ref[4],
        hit: true,
        openable: true,
      }
    }
    return plainRow(i, l)
  })
  return rows.some((r) => r.openable) ? rows : null
}

// parseOutputRows 是工具卡用的总入口：diff 与执行中的卡片不走这里（调用方先过滤）。
export function parseOutputRows(content: string, opts: { isSearch: boolean; searchOK: boolean }): OutputRow[] | null {
  if (!content.trim()) return null
  if (opts.isSearch && opts.searchOK) return parseSearchRows(content)
  if (opts.isSearch) return null
  return parseInlineRefs(content)
}
