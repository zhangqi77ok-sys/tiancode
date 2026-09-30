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

// ---- 稳定化（阶段 2）：流式增量不该让整表重渲染 ----
//
// 为什么需要：groupMessages 每次都返回全新对象，Vue 会认为每个 item 的 props 都变了，
// 于是整列子组件一起重渲染（长会话每 300ms 一次，与回复长度成正比）。
// 消息对象本身是**原地更新**的响应式对象（onChunk 里 `ast.content += delta`），
// 所以只要组成成员没变，就可以沿用上一轮的 item：其余子组件不重渲染，真正在流的那
// 一条靠它自己读到的字段变化驱动。
export function stabilizeItems(prev: RenderItem[], next: RenderItem[]): RenderItem[] {
  if (!prev.length) return next
  const byKey = new Map(prev.map((it) => [it.key, it]))
  return next.map((it) => {
    const old = byKey.get(it.key)
    if (!old || old.kind !== it.kind || !sameMembers(old, it)) return it
    return old
  })
}

function membersOf(it: RenderItem): ChatMsg[] {
  return it.kind === 'turn' ? it.run : [it.m]
}

// sameMembers 只比对象引用：成员增删（新工具卡、新段落）必然换新 item——
// 那种情况必须重渲染，否则新卡不会出现。
function sameMembers(a: RenderItem, b: RenderItem): boolean {
  const x = membersOf(a)
  const y = membersOf(b)
  if (x.length !== y.length) return false
  for (let i = 0; i < x.length; i++) {
    if (x[i] !== y[i]) return false
  }
  return true
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
