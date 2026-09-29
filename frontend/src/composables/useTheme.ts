// 主题三态（0.0.06）：auto（跟随系统 prefers-color-scheme）/ light / dark。
// 实现走 html[data-theme] 属性 + style.css 的令牌重定义——组件零改动。
// 偏好持久化在 localStorage；auto 时移除属性，让媒体查询接管。
export type ThemeMode = 'auto' | 'light' | 'dark'

const KEY = 'tiancode-theme'

function apply(mode: ThemeMode): void {
  const root = document.documentElement
  if (mode === 'auto') root.removeAttribute('data-theme')
  else root.setAttribute('data-theme', mode)
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
