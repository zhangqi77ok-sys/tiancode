// 回合窗口（阶段 2）：长会话只把视口附近的回合挂进 DOM。
//
// 为什么窗口贴着**尾部**一侧：贴底跟随、发送后到底、切换会话落最新——这三条纪律
// 都以最后一条消息为参照。尾部常挂，就不需要任何高度估算与占位对齐（那正是虚拟化
// 最容易出错的地方：错序、丢键、点不到卡）。老的一侧按块增挂；超过上限再按块裁掉，
// 且只在"用户贴在底部"时裁——绝不会把他正在看的内容抽走。
//
// 纯函数（有单测）：增挂/裁剪/复位三条规则不依赖 DOM，便于锁住边界。
export const WINDOW_INITIAL_TAIL = 40 // 首次进入（切换会话）只挂最后 40 条
export const WINDOW_CHUNK = 40 // 每次增挂 / 裁剪的块大小
export const WINDOW_MAX = 120 // 常挂上限：超过就从最老的一侧裁
export const WINDOW_GROW_NEAR_TOP_PX = 240 // 距挂载区顶部多少像素内就再往上补一块

// initialFrom 返回初始窗口起点：只挂尾部，历史多长都不一次性铺开。
export function initialFrom(total: number): number {
  return Math.max(0, total - WINDOW_INITIAL_TAIL)
}

// growFrom 向上增挂一块（起点变小）。
export function growFrom(from: number, total: number): number {
  return Math.max(0, Math.min(from, total) - WINDOW_CHUNK)
}

// shouldGrow 判断"用户已贴近挂载区顶部，该往上补一块了"。
export function shouldGrow(from: number, scrollTop: number): boolean {
  return from > 0 && scrollTop <= WINDOW_GROW_NEAR_TOP_PX
}

// trimFrom 在贴底且挂载数超过上限时裁掉最老的一块；不满足条件时原样返回。
// 为什么必须 anchored：用户在上方看历史时裁掉顶部会把他看的内容抽走。
export function trimFrom(from: number, total: number, anchored: boolean): number {
  if (!anchored) return from
  const keepFrom = Math.max(0, total - WINDOW_MAX)
  if (from >= keepFrom) return from
  return Math.min(from + WINDOW_CHUNK, keepFrom)
}
