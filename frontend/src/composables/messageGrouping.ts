import type { ChatMsg } from '../stores/chat'

// 消息列表渲染分组（纯函数，可单测）。
//
// 规则（0.2.16 用户反馈修正）：
// - 一个 agent 回合 = 从首个助手段起、直到下一条用户消息之前的**整段**：
//   助手段 / 工具卡 / 任务卡 / 问答卡 / 审批卡全部并入同一条 run——
//   视觉上是"一个输出"（单一 AGENT 头 + 内部按 ReAct 顺序排布的段落流），
//   但叙事顺序保留：思考 → 文本 → 工具卡 → 思考 → …
// - 孤立工具卡（回合被截断、前面没有助手）独立渲染，仍可折叠查看全文；
// - 所有卡（工具/任务/问答/审批）必须由所属 run 或独立分支恰好渲染一次，绝不重复。
export type RenderItem =
  | { kind: 'turn'; run: ChatMsg[]; key: string }
  | { kind: 'tool'; m: ChatMsg; key: string }
  | { kind: 'todo'; m: ChatMsg; key: string }
  | { kind: 'approval'; m: ChatMsg; key: string }
  | { kind: 'ask'; m: ChatMsg; key: string }
  | { kind: 'single'; m: ChatMsg; key: string }

export function keyOf(i: number, id?: string): string {
  return id ?? `i-${i}`
}

export function groupMessages(msgs: ChatMsg[]): RenderItem[] {
  const out: RenderItem[] = []
  for (let i = 0; i < msgs.length; i++) {
    const m = msgs[i]
    const key = keyOf(i, m.id)
    if (m.role === 'assistant') {
      // 一个回合：向后连续收集，直到下一条用户消息为止
      const run: ChatMsg[] = [m]
      let j = i + 1
      while (j < msgs.length && msgs[j].role !== 'user') {
        run.push(msgs[j])
        j++
      }
      out.push({ kind: 'turn', run, key })
      i = j - 1
    } else if (m.role === 'todo') {
      out.push({ kind: 'todo', m, key })
    } else if (m.role === 'approval') {
      out.push({ kind: 'approval', m, key })
    } else if (m.role === 'ask') {
      out.push({ kind: 'ask', m, key })
    } else if (m.role === 'tool') {
      // 能走到这里说明工具卡前面没有助手（回合被截断）：独立渲染
      out.push({ kind: 'tool', m, key })
    } else {
      out.push({ kind: 'single', m, key })
    }
  }
  return out
}
