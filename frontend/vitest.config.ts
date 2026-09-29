import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// Vitest 独立配置（0.2.31：组件级集成测试需要 vue 插件与 DOM 环境）。
// 为什么独立于 vite.config.ts：本次实测 vitest 未应用 vite.config 的插件链，
// .vue 文件按普通 JS 解析直接报错——显式声明本项目测试所需的两个前提：
//   1) @vitejs/plugin-vue：编译 SFC（真实 DOM 挂载的集成测试依赖它）；
//   2) jsdom：DOM 环境（store 单元测试在 jsdom 下同样可跑，无副作用）。
export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'jsdom',
  },
})
