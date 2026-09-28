import { describe, expect, it } from 'vitest'
import { groupMessages } from './messageGrouping'
import type { ChatMsg } from '../stores/chat'

function msg(partial: Partial<ChatMsg> & { role: ChatMsg['role'] }): ChatMsg {
  return { content: '', ...partial }
}

describe('groupMessages', () => {
  it('实时顺序：连续工具卡全部并入助手回合，不独立、不重复', () => {
    // 实流形态：工具卡被 splice 在流式助手之前，tool1..3 的下一个是兄弟工具卡
    const msgs = [
      msg({ role: 'user', id: 'm-1' }),
      msg({ role: 'tool', id: 'm-2', toolName: 'fs', content: 'file-1' }),
      msg({ role: 'tool', id: 'm-3', toolName: 'fs', content: 'file-2' }),
      msg({ role: 'tool', id: 'm-4', toolName: 'fs', content: 'file-3' }),
      msg({ role: 'tool', id: 'm-5', toolName: 'fs', content: 'file-4' }),
      msg({ role: 'assistant', id: 'm-6', content: '答' }),
    ]
    const items = groupMessages(msgs)
    expect(items).toHaveLength(2)
    expect(items[0]).toMatchObject({ kind: 'single', m: { id: 'm-1' } })
    expect(items[1]).toMatchObject({ kind: 'turn', m: { id: 'm-6' } })
    const turn = items[1] as Extract<(typeof items)[number], { kind: 'turn' }>
    expect(turn.tools.map((t) => t.id)).toEqual(['m-2', 'm-3', 'm-4', 'm-5'])
  })

  it('重放顺序：user → tool → assistant 同样并入', () => {
    const msgs = [
      msg({ role: 'user', id: 'm-1' }),
      msg({ role: 'tool', id: 'm-2', toolName: 'fs', content: 'file-x' }),
      msg({ role: 'assistant', id: 'm-3', content: 'done', thinking: 'plan' }),
    ]
    const items = groupMessages(msgs)
    expect(items).toHaveLength(2)
    expect(items[1]).toMatchObject({ kind: 'turn', tools: [msgs[1]] })
  })

  it('孤立工具卡（后面不跟助手）独立渲染为工具卡，不丢也不冒充气泡', () => {
    const msgs = [
      msg({ role: 'user', id: 'm-1' }),
      msg({ role: 'tool', id: 'm-2', toolName: 'fs', content: '截断轮次的工具输出' }),
    ]
    const items = groupMessages(msgs)
    expect(items).toHaveLength(2)
    expect(items[1]).toMatchObject({ kind: 'tool', m: { id: 'm-2' } })
  })

  it('审批卡不打断归组：approval 之后的工具卡仍并入助手回合', () => {
    const msgs = [
      msg({ role: 'user', id: 'm-1' }),
      msg({ role: 'approval', id: 'm-2', toolName: 'shell' }),
      msg({ role: 'tool', id: 'm-3', toolName: 'shell', content: 'out' }),
      msg({ role: 'assistant', id: 'm-4', content: '答' }),
    ]
    const items = groupMessages(msgs)
    expect(items).toHaveLength(3)
    expect(items[1]).toMatchObject({ kind: 'single', m: { role: 'approval' } })
    expect(items[2]).toMatchObject({ kind: 'turn', tools: [msgs[2]] })
  })

  it('多轮回合：各自的工具卡归属各自的助手', () => {
    const msgs = [
      msg({ role: 'user', id: 'm-1' }),
      msg({ role: 'tool', id: 'm-2', content: 'a' }),
      msg({ role: 'assistant', id: 'm-3', content: '答1' }),
      msg({ role: 'user', id: 'm-4' }),
      msg({ role: 'tool', id: 'm-5', content: 'b' }),
      msg({ role: 'assistant', id: 'm-6', content: '答2' }),
    ]
    const items = groupMessages(msgs)
    expect(items).toHaveLength(4)
    expect(items[1]).toMatchObject({ kind: 'turn', m: { id: 'm-3' }, tools: [msgs[1]] })
    expect(items[3]).toMatchObject({ kind: 'turn', m: { id: 'm-6' }, tools: [msgs[4]] })
  })

  it('缺 id 的消息回退下标 key（仅测试直插场景）', () => {
    const items = groupMessages([msg({ role: 'user' })])
    expect(items[0].key).toBe('i-0')
  })

  it('任务清单卡独立渲染，不打断工具卡归组', () => {
    const msgs = [
      msg({ role: 'user', id: 'm-1' }),
      msg({ role: 'todo', id: 'm-2' }),
      msg({ role: 'tool', id: 'm-3', content: 'out' }),
      msg({ role: 'assistant', id: 'm-4', content: '答' }),
    ]
    const items = groupMessages(msgs)
    expect(items).toHaveLength(3)
    expect(items[1]).toMatchObject({ kind: 'todo', m: { id: 'm-2' } })
    expect(items[2]).toMatchObject({ kind: 'turn', tools: [msgs[2]] })
  })
})
