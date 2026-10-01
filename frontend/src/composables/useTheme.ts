// 主题三态（0.0.06）：auto（跟随系统 prefers-color-scheme）/ light / dark。
// 实现走 html[data-theme] 属性 + style.css 的令牌重定义——组件零改动。
// 偏好持久化在 localStorage。
//
// auto 的解析（暗色令牌去重后）：style.css 的媒体查询兜底已移除，这里必须把 auto
// 解析成实际主题、data-theme 始终存在——CSS 只留一份深色块，改主题值不再要同步两处。
// 系统明暗翻转时由 matchMedia 监听即时重解析（仍处 auto 才生效，显式选择不被打扰）。
export type ThemeMode = 'auto' | 'light' | 'dark'

const KEY = 'tiancode-theme'

// 主题变化订阅（阶段 1）：跟着主题走的资源（代码高亮配色）要在切换时一起换。
// 回调不传参数：订阅方直接读 html[data-theme]——auto 已解析成实际主题写在属性上。
const listeners = new Set<() => void>()

function systemPrefersDark(): boolean {
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

function apply(mode: ThemeMode): void {
  const resolved = mode === 'auto' ? (systemPrefersDark() ? 'dark' : 'light') : mode
  document.documentElement.setAttribute('data-theme', resolved)
  if (mode === 'auto') watchSystemTheme()
  for (const cb of listeners) cb()
}

// 系统明暗监听：每次进 auto 都先摘旧的再挂新的（旧监听挂在旧 MQL 上，直接叠加会
// 随切换无限累积）；回调按当前偏好判重——用户已显式选 light/dark 后，系统翻转不再打扰。
let offSystem: (() => void) | null = null
function watchSystemTheme(): void {
  offSystem?.()
  const mql = window.matchMedia('(prefers-color-scheme: dark)')
  const onChange = () => {
    if (currentTheme() === 'auto') apply('auto')
  }
  mql.addEventListener('change', onChange)
  offSystem = () => mql.removeEventListener('change', onChange)
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
