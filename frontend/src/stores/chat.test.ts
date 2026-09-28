import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

const h = vi.hoisted(() => ({
  summaries: [] as { id: string; title: string }[],
  failRename: false,
  deleted: [] as string[],
  replay: [] as { role: string; content: string; toolName?: string; status?: string; thinking?: string }[],
  resolved: [] as string[],
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ListSessionSummaries: async () => h.summaries,
      Replay: async () => h.replay,
      Send: async () => {},
      Stop: async () => {},
      RenameSession: async () => {
        if (h.failRename) throw new Error('会话标题过长（最多 60 字）')
      },
      DeleteSession: async (id: string) => {
        h.deleted.push(id)
      },
      ApprovalPolicy: async () => [],
      SetApprovalPolicy: async () => {},
      ResolveApproval: async (id: string) => {
        h.resolved.push(id)
      },
    },
    runtime: { EventsOn: () => {} },
  }),
}))

const { useChatStore, END_REASON } = await import('./chat')

describe('chat store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    h.summaries = []
    h.failRename = false
    h.deleted = []
    h.replay = []
    h.resolved = []
  })

  // 审批卡片：原始参数原样落地 → 答复后卡片转已决（防重复点击）
  it('审批卡片插入与答复', async () => {
    const store = useChatStore()
    await store.newSession()
    store.onApproval({ id: 'ap-1', toolName: 'shell', arguments: '{"command":"rm -rf /tmp/x"}' })

    const card = store.messages[store.messages.length - 1]
    expect(card.role).toBe('approval')
    expect(card.toolName).toBe('shell')
    expect(card.args).toContain('rm -rf')

    await store.resolveApproval('ap-1', false, '危险命令')
    expect(h.resolved).toEqual(['ap-1'])
    expect(card.status).toBe('denied')
    expect(store.error).toBe('')
  })

  // 侧栏以摘要驱动：有标题显示标题，无标题回退会话 ID
  it('loadSessions 用摘要填充并支持标题回退', async () => {
    h.summaries = [
      { id: 's-1', title: '渠道排查' },
      { id: 's-2', title: '' },
    ]
    const store = useChatStore()
    await store.loadSessions()
    expect(store.sessions).toEqual(['s-1', 's-2'])
    expect(store.titleOf('s-1')).toBe('渠道排查')
    expect(store.titleOf('s-2')).toBe('s-2')
  })

  // 重命名失败必须可见（否则用户以为改好了）
  it('重命名失败写入错误', async () => {
    h.failRename = true
    const store = useChatStore()
    await store.renameSession('s-1', 'x'.repeat(61))
    expect(store.error).toContain('60')
  })

  // 删除当前会话后必须新建空会话，避免界面停在不存在的会话上
  it('删除当前会话后新建空会话', async () => {
    h.summaries = [{ id: 's-1', title: '' }]
    const store = useChatStore()
    await store.loadSessions()
    await store.selectSession('s-1')
    expect(store.sessionId).toBe('s-1')

    h.summaries = []
    await store.removeSession('s-1')
    expect(h.deleted).toEqual(['s-1'])
    expect(store.sessionId).not.toBe('s-1')
    expect(store.messages).toEqual([])
  })

  // 工具卡片携带结构化 diff（内核字段透传，UI 不解析文本）
  it('工具事件携带 diff', async () => {
    const store = useChatStore()
    await store.newSession()
    store.onTool({
      sessionID: store.sessionId,
      name: 'fs',
      status: 'success',
      summary: 'written a.txt',
      diff: '--- a.txt\n+++ a.txt\n@@ -1,1 +1,1 @@\n-one\n+ONE',
    })
    const card = store.messages[store.messages.length - 1]
    expect(card.diff).toContain('+ONE')
    expect(card.diff).toContain('-one')
  })

  // 终态错误要解开输入（running=false），否则用户被锁死无法继续
  it('错误终态解锁输入并标注消息', async () => {
    const store = useChatStore()
    await store.newSession()
    store.messages.push({ role: 'assistant', content: '半截', streaming: true })
    store.running = true
    store.onTerminal({ sessionID: store.sessionId, endReason: 2, error: '上游 500' })
    expect(store.running).toBe(false)
    expect(store.messages[0].error).toBe(true)
    expect(store.messages[0].content).toContain('上游 500')
  })

  it('onChunk 累加 thinking 与 delta', async () => {
    const store = useChatStore()
    await store.newSession()
    store.messages.push({ role: 'assistant', content: '', streaming: true })
    store.onChunk({ sessionID: store.sessionId, delta: 'Hi', thinking: 'plan' })
    expect(store.messages[0].content).toBe('Hi')
    expect(store.messages[0].thinking).toBe('plan')
  })

  it('onTool 保存全文 content', async () => {
    const store = useChatStore()
    await store.newSession()
    store.onTool({ sessionID: store.sessionId, name: 'fs', status: 'success', summary: 'ab…', content: 'abcdef' })
    expect(store.messages[0]).toMatchObject({ role: 'tool', toolName: 'fs', status: 'success', content: 'abcdef' })
  })

  // 工具卡插在流式助手之前后，后续 delta/thinking/终态仍必须落到该助手上
  it('工具卡插入后仍把流式增量写到助手消息', async () => {
    const store = useChatStore()
    await store.newSession()
    store.messages.push({ role: 'user', content: 'run' })
    store.messages.push({ role: 'assistant', content: '', streaming: true })
    store.running = true
    store.onTool({ sessionID: store.sessionId, name: 'fs', status: 'success', summary: 'ok', content: 'file' })
    store.onChunk({ sessionID: store.sessionId, delta: 'done', thinking: 'plan' })
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.DONE, error: '' })
    expect(store.messages.map((m) => m.role)).toEqual(['user', 'tool', 'assistant'])
    expect(store.messages[1]).toMatchObject({ role: 'tool', toolName: 'fs', content: 'file' })
    expect(store.messages[2].content).toBe('done')
    expect(store.messages[2].thinking).toBe('plan')
    expect(store.messages[2].streaming).toBe(false)
    expect(store.running).toBe(false)
  })

  it('selectSession 映射 Replay 的 tool 与 thinking', async () => {
    h.summaries = [{ id: 's-1', title: '' }]
    h.replay = [
      { role: 'user', content: 'u' },
      { role: 'tool', content: 'file-x', toolName: 'fs', status: 'success' },
      { role: 'assistant', content: 'done', thinking: 'plan' },
    ]
    const store = useChatStore()
    await store.loadSessions()
    await store.selectSession('s-1')
    // 只锁 Replay 映射契约：id 是入库时生成的稳定 key，不属于 Replay 契约，先剥掉再比
    expect(store.messages.map(({ id: _ignored, ...rest }) => rest)).toEqual([
      { role: 'user', content: 'u' },
      { role: 'tool', content: 'file-x', toolName: 'fs', status: 'success' },
      { role: 'assistant', content: 'done', thinking: 'plan' },
    ])
  })

  // 稳定 id 契约：列表 key 与折叠态的锚点；工具卡插入后既有消息的 key 不得改变
  it('每条入库消息都有唯一 id', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onTool({ sessionID: store.sessionId, name: 'fs', status: 'success', summary: 'ok' })
    store.onApproval({ id: 'ap-1', toolName: 'shell', arguments: '{}' })
    const ids = store.messages.map((m) => m.id)
    expect(ids).toHaveLength(4)
    expect(ids.every((x) => typeof x === 'string')).toBe(true)
    expect(new Set(ids).size).toBe(4)
  })
})

