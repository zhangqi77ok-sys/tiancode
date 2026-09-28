// 相对时间（开源聊天侧栏惯例）：刚刚 / N 分钟前 / N 小时前 / 昨天 / N 天前 / 具体日期。
// now 参数化以便确定性测试；非正或未来时间戳（全新会话）返回空串不显示。
const MIN_MS = 60_000
const HOUR_MS = 3_600_000
const DAY_MS = 86_400_000
const TWO_DAYS_MS = 2 * DAY_MS
const RECENT_DAYS = 30

export function relativeTime(ms: number, now = Date.now()): string {
  if (!ms || ms > now) return ''
  const diff = now - ms
  if (diff < MIN_MS) return '刚刚'
  if (diff < HOUR_MS) return `${Math.floor(diff / MIN_MS)} 分钟前`
  if (diff < DAY_MS) return `${Math.floor(diff / HOUR_MS)} 小时前`
  if (diff < TWO_DAYS_MS) return '昨天'
  if (diff < RECENT_DAYS * DAY_MS) return `${Math.floor(diff / DAY_MS)} 天前`
  const d = new Date(ms)
  return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()}`
}
