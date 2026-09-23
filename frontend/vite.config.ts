import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// Vite 配置：Vue3 + Tailwind4。产物输出到 dist/（gitignore，构建时生成）。
export default defineConfig({
  plugins: [vue(), tailwindcss()],
})
