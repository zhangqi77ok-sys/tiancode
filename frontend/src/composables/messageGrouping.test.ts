import { describe, expect, it } from 'vitest'
import { groupMessages, stabilizeItems } from './messageGrouping'
import type { ChatMsg } from '../stores/chat'

function msg(partial: Partial<ChatMsg> & { role: ChatMsg['role'] }): ChatMsg {
  return { content: '', ...partial }
}

// 收集一条 turn 内全部消息（含卡），供"恰好渲染一次"断言
function flat(run: ChatMsg[]): string[] {
  return run.map((m) => m.id ?? m.role)
}

describe('groupMessages', () => {
  it('一个回合合并为一个 run：助手段 → 工具卡 → 助手段 全并入', () => {
    // 0.2.14 起的真实实时顺序：工具卡封存在当前段之后
    const msgs = [
      msg({ role: 'user', id: 'm-1' }),
      msg({ role: 'assistant', id: 'm-2', content: '先看目录' }),
      msg({ role: 'tool', id: 'm-3', toolName: 'fs', content: 'file-1' }),
      msg({ role: 'tool', id: 'm-4', toolName: 'fs', content: 'file-2' }),
      msg({ role: 'assistant', id: 'm-5', content: '目录看完了' }),
    ]
    const items = groupMessages(msgs)
    expect(items).toHaveLength(2)
    expect(items[0]).toMatchObject({ kind: 'single', m: { id: 'm-1' } })
    expect(items[1].kind).toBe('turn')
    const run = items[1].kind === 'turn' ? items[1].run : []
    expect(flat(run)).toEqual(['m-2', 'm-3', 'm-4', 'm-5'])
  })

  it('重放顺序：user → 段1 → tool → 终段 同样收拢为一个 run', () => {
    const msgs = [
      msg({ role: 'user', id: 'm-1' }),
      msg({ role: 'assistant', id: 'm-2', content: 'x', thinking: 'plan-a' }),
      msg({ role: 'tool', id: 'm-3', toolName: 'fs', content: 'file-x' }),
      msg({ role: 'assistant', id: 'm-4', content: 'done', thinking: 'plan-b' }),
    ]
    const items = groupMessages(msgs)
    expect(items).toHaveLength(2)
    const run = items[1].kind === 'turn' ? items[1].run : []
    expect(flat(run)).toEqual(['m-2', 'm-3', 'm-4'])
  })

  it('两个用户回合各自成一条 run，绝不跨用户合并', () => {
    const msgs = [
      msg({ role: 'user', id: 'm-1' }),
      msg({ role: 'assistant', id: 'm-2', content: '答1' }),
      msg({ role: 'user', id: 'm-3' }),
      msg({ role: 'tool', id: 'm-4', content: 'b' }),
      msg({ role: 'assistant', id: 'm-5', content: '答2' }),
    ]
    const items = groupMessages(msgs)
    // [user, turn1, user, tool(孤立), turn2]
    expect(items).toHaveLength(5)
    const run1 = items[1].kind === 'turn' ? items[1].run : []
    const run2 = items[4].kind === 'turn' ? items[4].run : []
    expect(flat(run1)).toEqual(['m-2'])
    // 第二个回合从首个助手段开始：其前的孤立工具卡独立渲染
    expect(items[3]).toMatchObject({ kind: 'tool', m: { id: 'm-4' } })
    expect(flat(run2)).toEqual(['m-5'])
  })

  it('审批卡在回合内并入 run（叙事顺序保留），无助手时独立渲染', () => {
    const inTurn = [
      msg({ role: 'user', id: 'm-1' }),
      msg({ role: 'assistant', id: 'm-2', content: '要执行命令' }),
      msg({ role: 'approval', id: 'm-3', toolName: 'shell' }),
      msg({ role: 'tool', id: 'm-4', toolName: 'shell', content: 'out' }),
      msg({ role: 'assistant', id: 'm-5', content: '完成' }),
    ]
    const items = groupMessages(inTurn)
    expect(items).toHaveLength(2)
    const run = items[1].kind === 'turn' ? items[1].run : []
    expect(flat(run)).toEqual(['m-2', 'm-3', 'm-4', 'm-5'])

    const orphan = groupMessages([msg({ role: 'user', id: 'm-1' }), msg({ role: 'approval', id: 'm-2' })])
    expect(orphan[1]).toMatchObject({ kind: 'approval', m: { id: 'm-2' } })
  })

  it('任务清单/问答卡在回合内并入，无助手时各自独立', () => {
    const msgs = [
      msg({ role: 'user', id: 'm-1' }),
      msg({ role: 'assistant', id: 'm-2', content: '规划中' }),
      msg({ role: 'todo', id: 'm-3' }),
      msg({ role: 'tool', id: 'm-4', content: 'out' }),
      msg({ role: 'ask', id: 'm-5', question: '选哪个？' }),
      msg({ role: 'assistant', id: 'm-6', content: '继续' }),
    ]
    const items = groupMessages(msgs)
    expect(items).toHaveLength(2)
    const run = items[1].kind === 'turn' ? items[1].run : []
    expect(flat(run)).toEqual(['m-2', 'm-3', 'm-4', 'm-5', 'm-6'])

    const orphan = groupMessages([msg({ role: 'todo', id: 'm-1' })])
    expect(orphan[0]).toMatchObject({ kind: 'todo' })
  })

  it('孤立工具卡（回合被截断）独立渲染，不丢也不冒充气泡', () => {
    const msgs = [msg({ role: 'user', id: 'm-1' }), msg({ role: 'tool', id: 'm-2', toolName: 'fs', content: '截断轮次的工具输出' })]
    const items = groupMessages(msgs)
    expect(items).toHaveLength(2)
    expect(items[1]).toMatchObject({ kind: 'tool', m: { id: 'm-2' } })
  })

  it('缺 id 的消息回退下标 key（仅测试直插场景）', () => {
    const items = groupMessages([msg({ role: 'user' })])
    expect(items[0].key).toBe('i-0')
  })
})

// 阶段 2：稳定化——流式增量不该让整表重渲染（否则长会话每 300ms 全列重渲染一次）
describe('stabilizeItems', () => {
  const msgs = [
    msg({ role: 'user', id: 'm-1', content: '问' }),
    msg({ role: 'assistant', id: 'm-2', content: '答' }),
    msg({ role: 'user', id: 'm-3', content: '再问' }),
    msg({ role: 'assistant', id: 'm-4', content: '再答' }),
  ]

  it('成员没变时沿用上一轮的 item 对象（引用相等）', () => {
    const first = groupMessages(msgs)
    const second = stabilizeItems(first, groupMessages(msgs))
    expect(second).toHaveLength(first.length)
    second.forEach((it, i) => expect(it).toBe(first[i]))
  })

  it('原地改内容（流式增量）也不换对象——更新由消息自身的响应式驱动', () => {
    const first = groupMessages(msgs)
    msgs[3].content = '再答（变长）'
    const second = stabilizeItems(first, groupMessages(msgs))
    expect(second[3]).toBe(first[3]) // 该回合对象复用
    const run = second[3].kind === 'turn' ? second[3].run : []
    expect(run[0]).toBe(msgs[3]) // 复用的 run 里就是那条被原地更新的消息
  })

  it('成员增删（新工具卡/新段落）必须换新 item，否则新卡永远不出现', () => {
    const first = groupMessages(msgs)
    const grown = [...msgs, msg({ role: 'tool', id: 'm-5', toolName: 'fs', content: 'x' })]
    const second = stabilizeItems(first, groupMessages(grown))
    expect(second[3]).not.toBe(first[3]) // 该回合成员变了 → 必须重渲染
    expect(second[0]).toBe(first[0]) // 其它回合照旧复用
  })

  it('空列表与键变化都能安全处理', () => {
    const items = groupMessages(msgs)
    expect(stabilizeItems([], items)).toEqual(items)
    const replaced = [msg({ role: 'user', id: 'other', content: '换了一条' })]
    const out = stabilizeItems(items, groupMessages(replaced))
    expect(out[0].key).toBe('other')
  })
})
