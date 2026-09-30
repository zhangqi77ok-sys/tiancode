import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { initHljsTheme } from './composables/hljsTheme'
import { initTheme } from './composables/useTheme'
import './style.css'

// 应用入口：挂载 Pinia（M2 起承载会话状态）与根组件。
// 主题在挂载前应用（0.0.06）：避免深色用户每次启动先闪一帧浅色。
initTheme()
// 代码高亮配色跟着主题（阶段 1）：原来无条件引 github.css，深色下关键字/注释对比不够。
// 两份主题 CSS 由 composables/hljsTheme.ts 按当前主题动态挂载；底色仍归 .code-block。
initHljsTheme()
createApp(App).use(createPinia()).mount('#app')
