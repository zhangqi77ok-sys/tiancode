// 输入框 @ 文件引用（0.0.09）的纯逻辑：从 draft 与光标位置解析"正在输入的
// @ 词"，选中文件后完成替换。独立成纯函数便于单测。

export interface AtToken {
  // 命中时 start < end（[start, caret) 区间是 "@query"）；
  // query 为 @ 后已输入的部分（空串 = 刚敲下 @）
  start: number
  end: number
  query: string
}

// parseAtToken：caret 前最近的 @；@ 与 caret 之间不得有空白（否则不是文件名）；
// 紧邻 @ 前必须是行首或空白（避免把邮箱 someone@a.com 当引用）。
export function parseAtToken(draft: string, caret: number): AtToken | null {
  if (caret < 0 || caret > draft.length) return null
  const before = draft.slice(0, caret)
  const at = before.lastIndexOf('@')
  if (at < 0) return null
  const seg = before.slice(at) // "@query"
  if (seg.length > 1 && /\s/.test(seg.slice(1))) return null // 中途有空白：不是引用
  const prev = at > 0 ? before[at - 1] : ''
  if (prev !== '' && !/\s/.test(prev)) return null // 紧邻非空白（邮箱/半词）：不触发
  return { start: at, end: caret, query: seg.slice(1) }
}

// applyPick：把 [start, caret) 的 "@query" 替换为 "<path> "（尾随空格方便继续输入）。
export function applyPick(draft: string, caret: number, token: AtToken, path: string): { text: string; caret: number } {
  const ins = `${path} `
  const text = draft.slice(0, token.start) + ins + draft.slice(caret)
  return { text, caret: token.start + ins.length }
}
