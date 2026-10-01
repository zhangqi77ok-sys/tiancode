import { describe, expect, it } from 'vitest'
import { errText } from './errText'

// 错误文案化统一出口（此前全库 40+ 处手写 `String(e instanceof Error ? e.message : e)`）。
// 契约只有一条：Error 取 message，其余值走 String()——bridge/IPC 抛上来的不一定是 Error。
describe('errText 错误文案化', () => {
  it('Error 取 message', () => {
    expect(errText(new Error('保存扩展清单失败'))).toBe('保存扩展清单失败')
  })

  it('非 Error 值原样转字符串', () => {
    expect(errText('连接被拒绝')).toBe('连接被拒绝')
    expect(errText(42)).toBe('42')
    expect(errText({ code: 7 })).toBe('[object Object]')
  })
})
