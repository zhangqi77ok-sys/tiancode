import { describe, expect, it } from 'vitest'
import { consumeEsc, registerEsc } from './useEsc'

// Esc 消费栈（第 3 批）：浮层优先消费，没有浮层时才轮到全局中断生成。
describe('useEsc 消费栈', () => {
  it('后进先出：最后注册的消费者先处理', () => {
    const calls: string[] = []
    const off1 = registerEsc(() => calls.push('first'))
    const off2 = registerEsc(() => calls.push('second'))
    expect(consumeEsc()).toBe(true)
    expect(calls).toEqual(['second'])
    off2()
    expect(consumeEsc()).toBe(true)
    expect(calls).toEqual(['second', 'first'])
    off1()
    // 无消费者 → 返回 false，调用方（App.vue）才可以把 Esc 当"中断生成"
    expect(consumeEsc()).toBe(false)
  })

  it('注销后不再消费；重复注销幂等', () => {
    const off = registerEsc(() => {})
    off()
    off()
    expect(consumeEsc()).toBe(false)
  })
})
