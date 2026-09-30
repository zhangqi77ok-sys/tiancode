// 主题三态（0.0.06）：auto（跟随系统 prefers-color-scheme）/ light / dark。
// 实现走 html[data-theme] 属性 + style.css 的令牌重定义——组件零改动。
// 偏好持久化在 localStorage；auto 时移除属性，让媒体查询接管。
export type ThemeMode = 'auto' | 'light' | 'dark'

const KEY = 'tiancode-theme'

// 主题变化订阅（阶段 1）：跟着主题走的资源（代码高亮配色）要在切换时一起换。
// 回调不传参数：订阅方自己读 html[data-theme] / prefers-color-scheme——
// "跟随系统"模式下真正生效的明暗只有这两处知道。
const listeners = new Set<() => void>()

function apply(mode: ThemeMode): void {
  const root = document.documentElement
  if (mode === 'auto') root.removeAttribute('data-theme')
  else root.setAttribute('data-theme', mode)
  for (const cb of listeners) cb()
}

// onThemeChange 注册主题变化回调，返回取消订阅函数。
export function onThemeChange(cb: () => void): () => void {
  listeners.add(cb)
  return () => {
    listeners.delete(cb)
  }
}

export function currentTheme(): ThemeMode {
  return (localStorage.getItem(KEY) as ThemeMode | null) ?? 'auto'
}

export function setTheme(mode: ThemeMode): void {
  localStorage.setItem(KEY, mode)
  apply(mode)
}

export function initTheme(): void {
  apply(currentTheme())
}

export function cycleTheme(): ThemeMode {
  const order: ThemeMode[] = ['auto', 'light', 'dark']
  const next = order[(order.indexOf(currentTheme()) + 1) % order.length]
  setTheme(next)
  return next
}

export const THEME_LABEL: Record<ThemeMode, string> = {
  auto: '跟随系统',
  light: '浅色',
  dark: '深色',
}
