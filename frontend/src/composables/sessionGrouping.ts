import type { SessionSummaryDTO } from '../wails'

// 侧栏分区模型（纯函数，可单测）——三段式，对齐商用 AI 工具侧栏：
// ① 置顶：钉住的会话永远最上；② 会话：未归属空间的会话（对应参考图"任务"）；
// ③ 空间：按工作区分组（当前空间排最前、默认展开由组件控制，其余折叠）。
// 归属/置顶来自账本事件；组内按最后活跃降序（新建未发送会话视为最新、排最前）。
export interface SessionGroup {
  label: string
  workspace: string
  isCurrent: boolean
  items: SessionSummaryDTO[]
}

export interface SidebarSection {
  kind: 'pinned' | 'sessions' | 'spaces'
  label: string
  items: SessionSummaryDTO[] // pinned/sessions 区的会话
  groups: SessionGroup[] // spaces 区的空间分组（其余区为空数组）
}

function lastSegment(p: string): string {
  return p.split(/[\\/]/).filter(Boolean).pop() ?? ''
}

// 组内排序：最后活跃降序；无摘要（新建未发送）视为最新
function byRecency(a: SessionSummaryDTO, b: SessionSummaryDTO): number {
  return (b.lastActiveMs ?? Number.MAX_SAFE_INTEGER) - (a.lastActiveMs ?? Number.MAX_SAFE_INTEGER)
}

export function buildSidebar(
  summaries: SessionSummaryDTO[],
  ids: string[],
  currentPath: string,
): SidebarSection[] {
  const byId = new Map(summaries.map((s) => [s.id, s]))

  const pinned = summaries.filter((s) => s.pinned).sort(byRecency)
  const rest = summaries.filter((s) => !s.pinned)

  // 未归属空间的会话（workspace 为空）
  const untethered = rest.filter((s) => !s.workspace).sort(byRecency)

  // 空间分组：非置顶且有工作区的会话
  const spaceGroups = new Map<string, SessionGroup>()
  for (const sm of rest) {
    if (!sm.workspace) continue
    const label = lastSegment(sm.workspace)
    let g = spaceGroups.get(sm.workspace)
    if (!g) {
      g = { label, workspace: sm.workspace, isCurrent: sm.workspace === currentPath, items: [] }
      spaceGroups.set(sm.workspace, g)
    }
    g.items.push(sm)
  }
  for (const g of spaceGroups.values()) g.items.sort(byRecency)

  // 新建未发送的会话（无摘要）：有当前空间 → 该空间最上；未设置工作区 → "会话"区
  const pseudo = ids.filter((id) => !byId.has(id)).map((id) => ({ id, title: '' }))
  if (pseudo.length) {
    if (currentPath) {
      const label = lastSegment(currentPath)
      let g = spaceGroups.get(currentPath)
      if (!g) {
        g = { label, workspace: currentPath, isCurrent: true, items: [] }
        spaceGroups.set(currentPath, g)
      }
      g.isCurrent = true
      g.items = [...pseudo, ...g.items]
    } else {
      untethered.unshift(...pseudo)
    }
  }

  const spaces = [...spaceGroups.values()]
  spaces.sort((a, b) => (a.isCurrent ? -1 : b.isCurrent ? 1 : a.label.localeCompare(b.label)))

  const sections: SidebarSection[] = []
  if (pinned.length) sections.push({ kind: 'pinned', label: '置顶', items: pinned, groups: [] })
  if (untethered.length) sections.push({ kind: 'sessions', label: '会话', items: untethered, groups: [] })
  if (spaces.length) sections.push({ kind: 'spaces', label: '空间', items: [], groups: spaces })
  return sections
}
