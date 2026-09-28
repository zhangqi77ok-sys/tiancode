import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
// hljs 主题先于自有样式导入，便于 style.css 覆盖其底色/内边距
import 'highlight.js/styles/github.css'
import './style.css'

// 应用入口：挂载 Pinia（M2 起承载会话状态）与根组件。
createApp(App).use(createPinia()).mount('#app')
