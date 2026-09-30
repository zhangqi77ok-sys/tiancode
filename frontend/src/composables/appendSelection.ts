// 把对话区选中的文字追加到输入框草稿（第 7 批）的纯逻辑：独立成纯函数便于单测。
// 纪律：只追加、绝不替换整段草稿；草稿已有内容时空一行再追加；空选区返回原草稿。

export function appendToDraft(draft: string, selection: string): string {
  const sel = selection.trim()
  if (!sel) return draft
  const base = draft.replace(/\s+$/, '') // 先收掉草稿尾部空白，避免多出一堆空行
  return base ? `${base}\n\n${sel}` : sel
}
