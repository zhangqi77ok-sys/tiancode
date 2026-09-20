import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import './index.css'
import 'highlight.js/styles/atom-one-dark.css'

let uiMounted = false

function showBootError(err: unknown) {
  const msg = err instanceof Error ? `${err.message}\n${err.stack || ''}` : String(err)
  console.error('[tiancode]', err)
  if (uiMounted) return
  const el = document.getElementById('app')
  if (!el) return
  el.innerHTML = `<pre style="padding:24px;color:#b91c1c;white-space:pre-wrap;font:12px/1.5 ui-monospace,monospace">湉码启动失败:\n\n${msg}</pre>`
}

window.addEventListener('error', (e) => showBootError(e.error || e.message))
window.addEventListener('unhandledrejection', (e) => showBootError(e.reason))

try {
  const app = createApp(App)
  app.use(createPinia())
  app.config.errorHandler = (err) => showBootError(err)
  app.mount('#app')
  uiMounted = true
} catch (err) {
  showBootError(err)
}
