import type { SessionSummaryDTO } from '../wails'

// 侧栏按空间（工作区）分组（纯函数，可单测）。
// 参考商用 AI 工具的"空间"侧栏：当前空间排最前且默认展开，其余折叠；
// 归属 = 账本工作区快照（0.2.6 前的旧会话为空 → "未分组"）；
// 尚无摘要的新建会话（未发首条消息）归入当前空间最上方，不凭空消失。
export interface SessionGroup {
  label: string
  workspace: string
  isCurrent: boolean
  items: SessionSummaryDTO[]
}

function lastSegment(p: string): string {
  return p.split(/[\\/]/).filter(Boolean).pop() ?? ''
}

export function groupSessions(
  summaries: SessionSummaryDTO[],
  ids: string[],
  currentPath: string,
): SessionGroup[] {
  const byId = new Map(summaries.map((s) => [s.id, s]))
  const groups = new Map<string, SessionGroup>()
  const ensure = (workspace: string): SessionGroup => {
    const label = workspace ? lastSegment(workspace) : '未分组'
    let g = groups.get(label)
    if (!g) {
      g = { label, workspace, isCurrent: workspace === currentPath, items: [] }
      groups.set(label, g)
    }
    return g
  }

  for (const s of summaries) ensure(s.workspace ?? '').items.push(s)

  // 新建未发送的会话（无摘要）：归属语义上就是"在这里新建的" → 当前空间最上方
  const pseudo = ids.filter((id) => !byId.has(id)).map((id) => ({ id, title: '' }))
  if (pseudo.length) {
    const g = ensure(currentPath)
    g.items = [...pseudo, ...g.items]
  }

  const list = [...groups.values()]
  // 当前空间排最前，其余按标签排序保证列表稳定
  list.sort((a, b) => (a.isCurrent ? -1 : b.isCurrent ? 1 : a.label.localeCompare(b.label)))
  return list
}
