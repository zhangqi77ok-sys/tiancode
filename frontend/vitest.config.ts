import { defineConfig } from 'vitest/config'

// 测试必须有 DOM：markdown 消毒（DOMPurify）依赖真实 DOM 实现——
// 默认 node 环境下 DOMPurify.isSupported=false，sanitize 原样透传，
// 消毒契约在测试里形同虚设（TESTING.md「替身绕过真环节」的又一实例，2026-09-28 实测踩中）。
// 选 jsdom：devDependencies 现成，且是 DOMPurify 官方支持的 DOM 实现。
export default defineConfig({
  test: {
    environment: 'jsdom',
  },
})
