// diff 展示的共用规则（ToolCard / TurnReview / FileDetailPanel 单一来源）。
// 行着色只按前缀判定（diff 由内核生成，格式稳定）；+ / - 行用 -text 色（AA 达标），
// 装饰色只给功能色圆点与 @@ 头。

// diffLineClass 返回一行 diff 的文本色类。
export function diffLineClass(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'text-[var(--c-text-dim)]'
  if (line.startsWith('@@')) return 'text-[var(--c-primary)]'
  if (line.startsWith('+')) return 'text-[var(--c-ok-text)]'
  if (line.startsWith('-')) return 'text-[var(--c-err-text)]'
  return 'text-[var(--c-text-dim)]'
}

// diffStat 统计增删行（+++/--- 文件头不计）。
export function diffStat(diff: string | undefined): { add: number; del: number } {
  let add = 0
  let del = 0
  for (const line of (diff || '').split('\n')) {
    if (line.startsWith('+++') || line.startsWith('---')) continue
    if (line.startsWith('+')) add++
    else if (line.startsWith('-')) del++
  }
  return { add, del }
}
