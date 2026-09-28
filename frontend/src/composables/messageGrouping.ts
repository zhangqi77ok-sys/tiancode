import type { ChatMsg } from '../stores/chat'

// 消息列表渲染分组（纯函数，可单测）。
//
// 规则（0.2.3 实测事故的修正）：
// - 助手消息向前收集"紧邻的连续工具卡"并入同一回合块：思考 → 工具执行 → 回复正文；
// - 工具卡永远渲染为 ToolCard——绝不落入 MessageBubble（否则整份工具输出会被
//   渲染成助手气泡，实流中 tool1 的下一个是兄弟工具卡，曾把 go.mod 全文糊成回复）；
// - 一张工具卡要么并入回合块、要么独立渲染，绝不允许双重渲染；
// - 孤立工具卡（后面不再跟助手消息，如被截断的轮次）独立渲染，仍可折叠查看全文。
export type RenderItem =
  | { kind: 'turn'; m: ChatMsg; tools: ChatMsg[]; key: string }
  | { kind: 'tool'; m: ChatMsg; key: string }
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
      // 向前收集连续工具卡（遇到 user/approval/assistant 即停）
      const tools: ChatMsg[] = []
      for (let j = i - 1; j >= 0 && msgs[j].role === 'tool'; j--) tools.unshift(msgs[j])
      out.push({ kind: 'turn', m, tools, key })
    } else if (m.role === 'tool') {
      // 属于"后面紧跟助手消息的连续段"的工具卡由该助手统一渲染，这里跳过；
      // 判定方式：从本卡向后走完连续工具段，若段尾是助手消息则本卡必被并入
      let j = i
      while (msgs[j] && msgs[j].role === 'tool') j++
      if (msgs[j]?.role !== 'assistant') out.push({ kind: 'tool', m, key })
    } else {
      out.push({ kind: 'single', m, key })
    }
  }
  return out
}
