import { defineConfig } from 'vitest/config'

// store 测试需要 window（bridge() 读注入对象），因此用 happy-dom 而非 node 环境。
export default defineConfig({
  test: {
    environment: 'happy-dom',
    include: ['src/**/*.test.ts'],
  },
})
