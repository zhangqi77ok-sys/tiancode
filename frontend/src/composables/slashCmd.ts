// 输入框「/ 技能与 MCP」菜单（第 7 批）的纯逻辑：与 @ 文件引用同一层交互。
// 解析正在输入的 "/词"；选中后把 [start, caret) 整段删掉——选择体现在输入框上方的
// chip 上（正文里不留命令字样，模型也不该靠读正文猜）。

export interface SlashToken {
  start: number
  end: number
  query: string
}

// parseSlashToken：caret 前最近的 /；/ 与 caret 之间不得有空白（否则不是命令）；
// 紧邻 / 前必须是行首或空白（避免把 2026/09 这类内容当命令）。
export function parseSlashToken(draft: string, caret: number): SlashToken | null {
  if (caret < 0 || caret > draft.length) return null
  const before = draft.slice(0, caret)
  const at = before.lastIndexOf('/')
  if (at < 0) return null
  const seg = before.slice(at) // "/query"
  if (seg.length > 1 && /\s/.test(seg.slice(1))) return null
  const prev = at > 0 ? before[at - 1] : ''
  if (prev !== '' && !/\s/.test(prev)) return null
  return { start: at, end: caret, query: seg.slice(1) }
}

// applySlashPick：把 "/词" 整段删除（尾随内容原样保留，光标落在删除处）。
export function applySlashPick(draft: string, token: SlashToken): { text: string; caret: number } {
  const text = draft.slice(0, token.start) + draft.slice(token.end)
  return { text, caret: token.start }
}
