import type { ChatMsg, TodoItem } from '../stores/chat'

// 悬浮任务清单的纯逻辑（可单测）：位置钳制、持久化编解码、当前会话任务快照。
//
// 为什么抽成 composable 而不是直接写进组件：位置/边界的算术是这里唯一容易出错的部分，
// 而本仓库没有组件测试基建（无 @vue/test-utils）——只有抽出来才锁得住行为。
// 用户在实机上最可能遇到的坏情况是"拖动把卡片拖出可视区、再也找不回来"，
// 因此钳制函数必须有单测盯着。

export interface FloatPos {
  x: number
  y: number
}

export interface Size {
  w: number
  h: number
}

// 拖到边界时至少要留这么多像素可见：完全推出容器 = 用户找不回这块面板。
export const MIN_VISIBLE = 56

const POS_KEY = 'tiancode.todo.pos'
const COLLAPSED_KEY = 'tiancode.todo.collapsed'

// 默认位置：对话面板内的左上方（贴近任务卡原先内联出现的位置）。
export const DEFAULT_POS: FloatPos = { x: 20, y: 132 }

/** 把位置钳制在容器内（保留 MIN_VISIBLE 可见）；负数（拖出左上）同样被收回。 */
export function clampPos(pos: FloatPos, self: Size, bounds: Size): FloatPos {
  const keepX = Math.min(MIN_VISIBLE, self.w)
  const keepY = Math.min(MIN_VISIBLE, self.h)
  const maxX = Math.max(0, bounds.w - keepX)
  const maxY = Math.max(0, bounds.h - keepY)
  const clamp = (v: number, max: number) => Math.min(Math.max(0, v), max)
  return { x: clamp(pos.x, maxX), y: clamp(pos.y, maxY) }
}

/** 解析持久化的位置：缺失/损坏/非有限数一律返回 null（回退默认位，绝不因脏数据渲染出错）。 */
export function parsePos(raw: string | null | undefined): FloatPos | null {
  if (!raw) return null
  try {
    const v = JSON.parse(raw) as { x?: unknown; y?: unknown }
    if (typeof v?.x !== 'number' || typeof v?.y !== 'number') return null
    if (!Number.isFinite(v.x) || !Number.isFinite(v.y)) return null
    return { x: v.x, y: v.y }
  } catch {
    return null
  }
}

/** 当前会话的任务快照：取最后一条 todo 卡（实时事件与账本重放同源，同会话单卡原地更新）。
 * 0.0.11 生命周期：全部完成（或空清单）＝任务已收尾，悬浮件退场不再显示——
 * 账本重放恢复的旧快照同样适用，修"任务早完了清单还一直挂着"（用户实测）。 */
export function latestTodos(msgs: ChatMsg[]): { id: string; items: TodoItem[] } | null {
  for (let i = msgs.length - 1; i >= 0; i--) {
    if (msgs[i].role === 'todo') {
      const items = msgs[i].todos ?? []
      if (items.length === 0 || items.every((t) => t.status === 'done')) return null
      return { id: msgs[i].id ?? 'todo', items }
    }
  }
  return null
}

/** 拖动判定：位移超过阈值才算拖动，否则算点击——否则"点一下展开"会被手抖吞掉。 */
export function isDrag(dx: number, dy: number, threshold = 4): boolean {
  return Math.abs(dx) > threshold || Math.abs(dy) > threshold
}

/** 读取持久化位置（localStorage 不可用时降级为"不持久化"，不影响功能）。 */
export function loadPos(): FloatPos | null {
  try {
    return parsePos(localStorage.getItem(POS_KEY))
  } catch {
    return null
  }
}

export function savePos(pos: FloatPos): void {
  try {
    localStorage.setItem(POS_KEY, JSON.stringify(pos))
  } catch {
    // 位置不持久化：本次会话内拖动仍然有效，不值得为此打断用户操作
  }
}

/** 读取/保存折叠态（false = 展开列表）。 */
export function loadCollapsed(): boolean {
  try {
    return localStorage.getItem(COLLAPSED_KEY) === '1'
  } catch {
    return false
  }
}

export function saveCollapsed(v: boolean): void {
  try {
    localStorage.setItem(COLLAPSED_KEY, v ? '1' : '0')
  } catch {
    // 同上：折叠态不持久化不影响使用
  }
}

const HIDDEN_KEY = 'tiancode.todo.hidden'

/** 读取/保存"用户隐藏"态（隐藏后出现新任务清单会自动恢复显示）。 */
export function loadHidden(): boolean {
  try {
    return localStorage.getItem(HIDDEN_KEY) === '1'
  } catch {
    return false
  }
}

export function saveHidden(v: boolean): void {
  try {
    localStorage.setItem(HIDDEN_KEY, v ? '1' : '0')
  } catch {
    // 不持久化不影响使用
  }
}
