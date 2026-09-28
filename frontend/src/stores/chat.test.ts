import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

const h = vi.hoisted(() => ({
  summaries: [] as { id: string; title: string }[],
  failRename: false,
  deleted: [] as string[],
  renamed: [] as { id: string; title: string }[],
  replay: [] as {
    role: string
    content: string
    toolName?: string
    status?: string
    thinking?: string
    title?: string
    op?: string
    diff?: string
  }[],
  resolved: [] as string[],
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ListSessionSummaries: async () => h.summaries,
      Replay: async () => h.replay,
      Send: async () => {},
      Stop: async () => {},
      RenameSession: async (id: string, title: string) => {
        if (h.failRename) throw new Error('会话标题过长（最多 60 字）')
        h.renamed.push({ id, title })
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
const { useWorkspaceStore } = await import('./workspace')

describe('chat store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    h.summaries = []
    h.failRename = false
    h.deleted = []
    h.renamed = []
    h.replay = []
    h.resolved = []
  })

  // 开源惯例（open-webui/lobe-chat）：首轮结束用首条消息截断自动命名，侧栏不再裸奔会话 ID
  it('首轮结束自动命名会话（首条消息截断 20 字）', async () => {
    const store = useChatStore()
    await store.newSession()
    const long = '这是一条很长很长的第一条消息用来验证自动命名会截断到二十个字为止后续不进入标题'
    await store.send(long)
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.DONE, error: '' })
    await new Promise((r) => setTimeout(r, 0))
    expect(h.renamed).toHaveLength(1)
    expect(h.renamed[0].id).toBe(store.sessionId)
    expect(h.renamed[0].title.length).toBeLessThanOrEqual(20)
    expect(h.renamed[0].title).not.toContain('\n')
    // send() 起算的轮次耗时在 terminal 时落到助手消息上（send 直插场景才有）
    expect(store.messages.find((m) => m.role === 'assistant')?.durationMs).toBeGreaterThanOrEqual(0)
  })

  it('已有标题的会话终态后不自动覆盖', async () => {
    h.summaries = [{ id: 's-x', title: '我的标题' }]
    const store = useChatStore()
    await store.loadSessions() // 镜像真实 init 路径：summaries 先就位，titleOf 才能判出"已命名"
    await store.selectSession('s-x')
    await store.send('新问题')
    store.onTerminal({ sessionID: 's-x', endReason: END_REASON.DONE, error: '' })
    await new Promise((r) => setTimeout(r, 0))
    expect(h.renamed).toHaveLength(0)
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

  // 删除当前会话后必须回到草稿态，避免界面停在不存在的会话上
  it('删除当前会话后回到草稿态', async () => {
    h.summaries = [{ id: 's-1', title: '' }]
    const store = useChatStore()
    await store.loadSessions()
    await store.selectSession('s-1')
    expect(store.sessionId).toBe('s-1')

    h.summaries = []
    await store.removeSession('s-1')
    expect(h.deleted).toEqual(['s-1'])
    expect(store.sessionId).toBe('')
    expect(store.messages).toEqual([])
  })

  // 0.2.9 回归修复：新会话是草稿——不占 ID、不进侧栏；反复"打开工作区/新建对话"
  // 不得堆积空会话（伪会话曾被分组归到当前空间，重启才消失）。首聊发出时才领 ID 落地
  it('newSession 是草稿：不占 ID 不进侧栏，首聊才落地', async () => {
    h.summaries = [{ id: 's-1', title: '已有' }]
    const store = useChatStore()
    await store.loadSessions()
    await store.newSession()
    await store.newSession() // 模拟连续切换工作区/新建对话
    expect(store.sessionId).toBe('')
    expect(store.sessions).toEqual(['s-1']) // 侧栏镜像账本，无伪会话堆积
    await store.send('hi')
    expect(store.sessionId).not.toBe('')
  })

  // 0.2.10 反馈修复：会话要在首聊发出的瞬间就进侧栏并归属当前工作区，不等回合结束
  //（长任务跑完前侧栏看不到它）。本地待定摘要随回合后的 loadSessions 被账本真实数据校正
  it('首聊即时入列：send 瞬间侧栏可见并归属当前工作区', async () => {
    useWorkspaceStore().path = 'D:/w/beta'
    const store = useChatStore()
    await store.send('hi')
    const entry = store.summaries.find((s) => s.id === store.sessionId)
    expect(entry?.workspace).toBe('D:/w/beta')
    expect(entry?.title).toBe('') // 未命名 → 终态后走自动命名
    // 回合结束后 loadSessions 用账本数据校正（mock 后端列表为空 → 本地待定条目被替换）
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.DONE, error: '' })
    await new Promise((r) => setTimeout(r, 0))
    expect(store.summaries.some((s) => s.id === store.sessionId)).toBe(false)
  })

  // 工具卡片携带结构化 diff 与语义标签（内核字段透传，UI 不解析文本）
  it('工具事件携带 diff 与 title/op', async () => {
    const store = useChatStore()
    await store.newSession()
    store.onTool({
      sessionID: store.sessionId,
      name: 'fs',
      status: 'success',
      summary: 'written a.txt',
      title: 'a.txt',
      op: 'write',
      diff: '--- a.txt\n+++ a.txt\n@@ -1,1 +1,1 @@\n-one\n+ONE',
    })
    const card = store.messages[store.messages.length - 1]
    expect(card.diff).toContain('+ONE')
    expect(card.diff).toContain('-one')
    expect(card.title).toBe('a.txt')
    expect(card.op).toBe('write')
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
      {
        role: 'tool',
        content: 'file-x',
        toolName: 'fs',
        status: 'success',
        title: 'install.go',
        op: 'edit',
        diff: '+x',
      },
      { role: 'assistant', content: 'done', thinking: 'plan' },
    ]
    const store = useChatStore()
    await store.loadSessions()
    await store.selectSession('s-1')
    // 只锁 Replay 映射契约：id 是入库时生成的稳定 key，不属于 Replay 契约，先剥掉再比
    expect(store.messages.map(({ id: _ignored, ...rest }) => rest)).toEqual([
      { role: 'user', content: 'u' },
      {
        role: 'tool',
        content: 'file-x',
        toolName: 'fs',
        status: 'success',
        title: 'install.go',
        op: 'edit',
        diff: '+x',
      },
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

