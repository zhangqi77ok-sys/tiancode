import githubLight from 'highlight.js/styles/github.css?inline'
import githubDark from 'highlight.js/styles/github-dark.css?inline'
import { onThemeChange } from './useTheme'

// 代码高亮配色跟随主题（阶段 1）。
//
// 为什么不再在 main.ts 里无条件 import github.css：那份配色是给浅底写的，深色下
// 关键字与注释对比不够（用户反馈）。两份主题 CSS 以字符串引入（Vite ?inline），
// 运行时挂进同一个 <style>，随主题切换换内容——不新增依赖，也不需要在两个主题
// 之间做优先级博弈。
//
// 为什么只换配色、不换底色：.hljs 的背景由 style.css 的 .code-block 统一负责
// （选择器更具体，压得住主题里的 .hljs 底色），避免出现"代码块里再套一层底色"。

const STYLE_ID = 'hljs-theme'

// effectiveTheme 取当前生效的明暗：data-theme 明确指定时用它，否则问系统。
function effectiveTheme(): 'light' | 'dark' {
  const attr = document.documentElement.getAttribute('data-theme')
  if (attr === 'dark' || attr === 'light') return attr
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function apply(): void {
  let el = document.getElementById(STYLE_ID) as HTMLStyleElement | null
  if (!el) {
    el = document.createElement('style')
    el.id = STYLE_ID
    document.head.appendChild(el)
  }
  el.textContent = effectiveTheme() === 'dark' ? githubDark : githubLight
}

// initHljsTheme 在挂载前调用一次：立即挂上当前主题的配色，并订阅后续变化
// （用户切主题、系统主题变化都要跟上——跟随系统模式下后者是唯一的触发源）。
export function initHljsTheme(): void {
  apply()
  onThemeChange(() => apply())
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => apply())
}
