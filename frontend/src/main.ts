import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import './style.css'

// 应用入口：挂载 Pinia（M2 起承载会话状态）与根组件。
createApp(App).use(createPinia()).mount('#app')
