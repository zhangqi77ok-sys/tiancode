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
    question?: string
    options?: string[]
  }[],
  // 多会话：按会话 ID 定制重放（缺省回落 h.replay）
  replayById: {} as Record<string, { role: string; content: string }[]>,
  // 可控延迟：模拟 Replay 的 IPC 往返窗口（窗口内发送的合并测试用）
  replayGate: null as Promise<void> | null,
  resolved: [] as string[],
  sends: [] as string[],
  // 多会话：记录每次 Send 的目标会话（断言后台会话的队列续发归属）
  sendCalls: [] as { sessionID: string; text: string }[],
  resolvedAsks: [] as { id: string; answer: string }[],
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ListSessionSummaries: async () => h.summaries,
      Replay: async (id: string) => {
        if (h.replayGate) await h.replayGate
        return h.replayById[id] ?? h.replay
      },
      Send: async (sessionID: string, text: string) => {
        h.sendCalls.push({ sessionID, text })
        h.sends.push(text)
      },
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
      ResolveAsk: async (id: string, answer: string) => {
        h.resolvedAsks.push({ id, answer })
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
    h.replayById = {}
    h.replayGate = null
    h.resolved = []
    h.sends = []
    h.sendCalls = []
    h.resolvedAsks = []
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
    // 0.2.14 起助手消息按 ReAct 轮次按需分段创建，本测试无流式块 → 无助手段
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
    await store.send('hi') // 先领真实会话 ID（草稿态空 ID 的卡片会被契约丢弃）
    store.onApproval({
      id: 'ap-1',
      sessionID: store.sessionId,
      toolName: 'shell',
      arguments: '{"command":"rm -rf /tmp/x"}',
    })

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

  // 零块终态：整回合一条增量都没到达就失败（如无可用渠道 / 流未建立）。
  // 此前该路径的错误被静默丢弃——用户看到的是"消息发出去了，什么都没发生"（实机反馈）
  it('零块错误终态必须新建可见错误气泡', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('介绍这个项目')
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.ERROR, error: '无可用模型渠道' })
    expect(store.running).toBe(false)
    const last = store.messages.at(-1)
    expect(last?.role).toBe('assistant')
    expect(last?.error).toBe(true)
    expect(last?.content).toContain('无可用模型渠道')
    expect(store.messages).toHaveLength(2) // user + 错误气泡
  })

  // 零块空闲超时同理：上游挂起、一条增量都没来，也必须看得见
  it('零块空闲超时也可见', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.IDLE_TIMEOUT, error: '' })
    expect(store.messages.at(-1)?.content).toContain('响应超时')
  })

  // 反向契约：正常结束且无增量（例如模型只调用了工具）不得补空气泡
  it('零块正常终态不补空气泡', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.DONE, error: '' })
    expect(store.messages.filter((m) => m.role === 'assistant')).toHaveLength(0)
  })

  // 0.2.28：发送即有"正在思考"占位——慢中转/上游挂起时用户立刻有反馈
  //（实机：Send 在等响应头阶段永久挂起，界面毫无动静像死机）。占位由
  // onChunk 复用；零块 DONE 后移除空占位（不留空气泡）。
  it('发送即创建思考占位，首块复用，零块 DONE 后移除', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    expect(store.messages.map((m) => m.role)).toEqual(['user', 'assistant'])
    expect(store.messages[1].streaming).toBe(true)
    expect(store.messages[1].content).toBe('')
    // 首个增量复用占位段（不另开新段）
    store.onChunk({ sessionID: store.sessionId, delta: '你好', thinking: '' })
    expect(store.messages).toHaveLength(2)
    expect(store.messages[1].content).toBe('你好')
    // 零块 DONE：空占位移除
    const store2 = useChatStore()
    await store2.newSession()
    await store2.send('hi')
    store2.onTerminal({ sessionID: store2.sessionId, endReason: END_REASON.DONE, error: '' })
    expect(store2.messages.filter((m) => m.role === 'assistant')).toHaveLength(0)
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

  // 工具卡封存当前段并插其后，后续 delta/thinking/终态必须落到新开的段上
  it('直插场景下工具卡不吞后续流式增量', async () => {
    const store = useChatStore()
    await store.newSession()
    store.messages.push({ role: 'user', content: 'run' })
    store.messages.push({ role: 'assistant', content: '半段', streaming: true })
    store.running = true
    store.onTool({ sessionID: store.sessionId, name: 'fs', status: 'success', summary: 'ok', content: 'file' })
    store.onChunk({ sessionID: store.sessionId, delta: 'done', thinking: 'plan' })
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.DONE, error: '' })
    expect(store.messages.map((m) => m.role)).toEqual(['user', 'assistant', 'tool', 'assistant'])
    expect(store.messages[1]).toMatchObject({ content: '半段', streaming: false })
    expect(store.messages[2]).toMatchObject({ role: 'tool', toolName: 'fs', content: 'file' })
    expect(store.messages[3].content).toBe('done')
    expect(store.messages[3].thinking).toBe('plan')
    expect(store.messages[3].streaming).toBe(false)
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
    store.onApproval({ id: 'ap-1', sessionID: store.sessionId, toolName: 'shell', arguments: '{}' })
    // 0.2.28：send 即建"正在思考"占位段（工具卡封存它并插其后）→ user + 占位 + tool + approval 四条
    const ids = store.messages.map((m) => m.id)
    expect(ids).toHaveLength(4)
    expect(ids.every((x) => typeof x === 'string')).toBe(true)
    expect(new Set(ids).size).toBe(4)
  })

  // ReAct 段落化：工具事件封存当前段并插卡其后，后续增量落新段——
  // 叙事顺序 = 本轮思考/文本 → 工具卡 → 下一段（不再全部堆进一个气泡）
  it('工具事件封存当前段，后续增量开新段', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onChunk({ sessionID: store.sessionId, delta: '先看一眼', thinking: '思考一' })
    store.onTool({ sessionID: store.sessionId, name: 'fs', status: 'success', summary: 'ok', title: 'a.txt', op: 'read' })
    store.onChunk({ sessionID: store.sessionId, delta: '再看', thinking: '思考二' })
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.DONE, error: '' })
    expect(store.messages.map((m) => m.role)).toEqual(['user', 'assistant', 'tool', 'assistant'])
    expect(store.messages[1]).toMatchObject({ content: '先看一眼', thinking: '思考一', streaming: false })
    expect(store.messages[2]).toMatchObject({ role: 'tool', title: 'a.txt', op: 'read' })
    expect(store.messages[3]).toMatchObject({ content: '再看', thinking: '思考二', streaming: false })
    // 轮次耗时落到最后一段助手消息上（send 起算、terminal 收算）
    expect(store.messages[3].durationMs).toBeGreaterThanOrEqual(0)
  })

  // 任务清单：单卡原地更新（同会话只保留一张，位置保留首次出现处）
  it('onTodo 插入并原地更新任务卡', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onTodo({
      sessionID: store.sessionId,
      items: [
        { text: 'a', status: 'pending' },
        { text: 'b', status: 'in_progress' },
      ],
    })
    store.onTodo({ sessionID: store.sessionId, items: [{ text: 'a', status: 'done' }] })
    const todoCards = store.messages.filter((m) => m.role === 'todo')
    expect(todoCards).toHaveLength(1)
    expect(todoCards[0].todos).toEqual([{ text: 'a', status: 'done' }])
  })

  // todo 工具卡不再重复渲染（任务卡由悬浮件 FloatingTodo 承载）
  it('onTool 忽略 todo 工具', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onTool({ sessionID: store.sessionId, name: 'todo', status: 'success', summary: 'updated' })
    expect(store.messages.filter((m) => m.role === 'tool')).toHaveLength(0)
  })

  // 问答卡：chat:ask 插入待答卡并封存当前段；resolveAsk 成功后转已答态展示所选答案
  it('onAsk 插入问答卡，resolveAsk 回流答复', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onChunk({ sessionID: store.sessionId, delta: '需要确认', thinking: '' })
    store.onAsk({ id: 'ask-1', sessionID: store.sessionId, question: '选哪个方案？', options: ['方案 A', '方案 B'] })
    const card = store.messages.at(-1)
    expect(card).toMatchObject({ role: 'ask', question: '选哪个方案？', askId: 'ask-1' })
    expect(card?.answered).toBeFalsy()
    expect(store.messages.at(-2)?.streaming).toBe(false) // 当前段已封存
    await store.resolveAsk('ask-1', '方案 A')
    expect(h.resolvedAsks).toEqual([{ id: 'ask-1', answer: '方案 A' }])
    expect(card?.answered).toBe(true)
    expect(card?.content).toBe('方案 A')
  })

  // ask_user 工具卡不重复渲染（问答卡已承载）
  it('onTool 忽略 ask_user 工具', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onTool({ sessionID: store.sessionId, name: 'ask_user', status: 'success', summary: 'answered' })
    expect(store.messages.filter((m) => m.role === 'tool')).toHaveLength(0)
  })

  // 0.2.26 回归：isRunning 是侧栏渲染期的高频调用，必须**只读**——
  // 若顺手创建会话缓冲，selectSession 的"无缓冲才 Replay"判断被打假，
  // 历史会话永远打不开（实机：点开任何旧会话都是空白）。
  it('isRunning 只读不创建缓冲，历史会话可被 Replay', async () => {
    h.summaries = [{ id: 's-1', title: '历史' }]
    h.replay = [{ role: 'user', content: '旧消息' }]
    const store = useChatStore()
    await store.loadSessions()
    // 侧栏渲染路径：对每条会话问"是否在跑"（不得产生缓冲）
    expect(store.isRunning('s-1')).toBe(false)
    await store.selectSession('s-1')
    // Replay 必须真的执行（缓冲此前不存在）
    expect(store.messages.map((m) => m.content)).toEqual(['旧消息'])
  })

  // 0.2.26 契约：审批/问答事件缺 sessionID 时必须丢弃并报错（绝不插进当前视图＝串会话）
  it('缺 sessionID 的审批/问答事件被丢弃且可见', async () => {
    const store = useChatStore()
    await store.newSession()
    store.onApproval({ id: 'ap-x', sessionID: '', toolName: 'shell', arguments: '{}' })
    expect(store.messages.filter((m) => m.role === 'approval')).toHaveLength(0)
    expect(store.error).toContain('缺少会话标识')
    store.onAsk({ id: 'ask-x', sessionID: '', question: '?' })
    expect(store.messages.filter((m) => m.role === 'ask')).toHaveLength(0)
    expect(store.error).toContain('缺少会话标识')
  })

  // Replay 恢复问答卡为已答态（问题/选项由账本 tool_call 配对投影）
  it('Replay 恢复问答卡为已答态', async () => {
    h.summaries = [{ id: 's-1', title: '' }]
    h.replay = [
      { role: 'user', content: 'u' },
      { role: 'ask', content: '方案 A', question: '选哪个？', options: ['方案 A'] },
    ]
    const store = useChatStore()
    await store.loadSessions()
    await store.selectSession('s-1')
    expect(store.messages[1]).toMatchObject({
      role: 'ask',
      content: '方案 A',
      question: '选哪个？',
      answered: true,
    })
  })

  // 输入队列：running 期间的提交入队，终态后自动逐条发出
  it('终态后自动发出队列首条', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('第一条')
    store.enqueue('排队的第二条')
    store.enqueue('排队的第三条')
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.DONE, error: '' })
    await new Promise((r) => setTimeout(r, 0))
    expect(h.sends).toEqual(['第一条', '排队的第二条']) // 首条 send 也经桥记录
    expect(store.queue.map((q) => q.text)).toEqual(['排队的第三条'])
    // 发出后消息流进入下一轮：新用户消息 + 其"正在思考"占位（0.2.28）
    expect(store.messages.at(-1)?.role).toBe('assistant')
    expect(store.messages.at(-1)?.streaming).toBe(true)
    expect(store.messages.at(-2)).toMatchObject({ role: 'user', content: '排队的第二条' })
  })

  it('取消终态不自动发出队列', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('第一条')
    store.enqueue('排队的第二条')
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.CANCELLED, error: 'cancelled' })
    await new Promise((r) => setTimeout(r, 0))
    expect(h.sends).toEqual(['第一条'])
    expect(store.queue.map((q) => q.text)).toEqual(['排队的第二条'])
    expect(store.running).toBe(false)
  })

  it('队列置顶/取回编辑/删除', async () => {
    const store = useChatStore()
    store.enqueue('一')
    store.enqueue('二')
    store.enqueue('三')
    store.promoteQueued(store.queue[2].id) // 三 → 最前
    expect(store.queue.map((q) => q.text)).toEqual(['三', '一', '二'])
    const t = store.editQueued(store.queue[0].id)
    expect(t).toBe('三')
    expect(store.queue.map((q) => q.text)).toEqual(['一', '二'])
    store.removeQueued(store.queue[0].id)
    expect(store.queue.map((q) => q.text)).toEqual(['二'])
  })

  // —— 多会话并行（0.2.25）：运行中自由切换，事件各归各位 ——

  it('后台会话的增量落在它自己的缓冲里，切回去原地接着看', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('A 的第一问')
    const a = store.sessionId
    await store.newSession() // A 还在跑也允许新建（多会话核心诉求）
    await store.send('B 的问题')
    // A 的增量/工具事件到达时，正在看的是 B（B 的缓冲 = user + 思考占位）
    store.onChunk({ sessionID: a, delta: 'A 回答', thinking: '' })
    store.onTool({ sessionID: a, name: 'fs', status: 'success', summary: 'ok' })
    expect(store.messages.map((m) => m.content)).toEqual(['B 的问题', '']) // 当前视图不含 A 的内容
    await store.selectSession(a) // 切回 A：缓冲原样保留（不重放、不覆盖）
    // A 的首个增量复用了"正在思考"占位段（0.2.28），叙事 = user → 回答 → 工具卡
    expect(store.messages.map((m) => m.content)).toEqual(['A 的第一问', 'A 回答', 'ok'])
    expect(store.messages.some((m) => m.role === 'tool')).toBe(true)
  })

  it('后台会话的终态不丢也不串：A 结束不影响 B 的运行', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('A 问')
    const a = store.sessionId
    await store.newSession()
    await store.send('B 问')
    store.onChunk({ sessionID: a, delta: 'A 半截', thinking: '' })
    store.onTerminal({ sessionID: a, endReason: END_REASON.ERROR, error: '渠道故障' })
    expect(store.isRunning(a)).toBe(false)
    expect(store.isRunning(store.sessionId)).toBe(true) // B 仍在后台跑
    // 当前视图不受 A 终态影响：B 的缓冲里没有任何 A 的内容
    expect(store.messages.some((m) => m.content === 'B 问')).toBe(true)
    expect(store.messages.some((m) => m.content.includes('渠道故障'))).toBe(false)
    await store.selectSession(a)
    const last = store.messages.at(-1)
    expect(last?.error).toBe(true)
    expect(last?.content).toContain('渠道故障')
  })

  it('审批卡按 sessionID 归位到后台会话', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('A')
    const a = store.sessionId
    await store.newSession()
    await store.send('B')
    store.onApproval({ id: 'ap-9', sessionID: a, toolName: 'shell', arguments: '{}' })
    expect(store.messages.some((m) => m.role === 'approval')).toBe(false) // 不插进当前视图
    await store.selectSession(a)
    expect(store.messages.some((m) => m.approvalId === 'ap-9')).toBe(true)
  })

  it('后台会话的队列续发仍发给它自己', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('A 第一条')
    const a = store.sessionId
    store.enqueue('A 排队第二条')
    await store.newSession()
    await store.send('B 的问题')
    store.onTerminal({ sessionID: a, endReason: END_REASON.DONE, error: '' })
    await new Promise((r) => setTimeout(r, 0))
    expect(h.sendCalls.at(-1)).toEqual({ sessionID: a, text: 'A 排队第二条' })
  })

  it('删除运行中的会话被拒绝且错误可见', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('跑着呢')
    const id = store.sessionId
    await store.removeSession(id)
    expect(store.error).toContain('正在运行')
    expect(h.deleted).toEqual([])
  })

  // 0.2.30：Replay 窗口（IPC 往返）内用户已发出的消息不能被历史整体覆盖丢掉
  //（实机：冷启动直接对话，"发送后什么都没显示"，切换会话才恢复）。
  it('Replay 窗口内的发送不丢：历史前置合并', async () => {
    h.summaries = [{ id: 's-1', title: '旧会话' }]
    h.replayById['s-1'] = [{ role: 'user', content: '旧问题' }]
    let release: () => void = () => {}
    h.replayGate = new Promise<void>((r) => {
      release = r
    })

    const store = useChatStore()
    await store.loadSessions()
    const sel = store.selectSession('s-1') // 挂起在 Replay（模拟 IPC 窗口）
    await store.send('窗口里发的新消息')
    release()
    await sel
    // 历史在前；窗口内发送的 user 与其思考占位都在，且不叠重复
    expect(store.messages.map((m) => m.content)).toEqual(['旧问题', '窗口里发的新消息', ''])
    expect(store.messages.filter((m) => m.content === '窗口里发的新消息')).toHaveLength(1)
  })

  it('Replay 晚于落账（历史已含该消息）时不叠重复', async () => {
    h.summaries = [{ id: 's-1', title: '旧会话' }]
    let release: () => void = () => {}
    h.replayGate = new Promise<void>((r) => {
      release = r
    })

    const store = useChatStore()
    await store.loadSessions()
    const sel = store.selectSession('s-1')
    await store.send('窗口里发的新消息')
    // 后端已落账：Replay 投影里已含这条 user（历史尾部与本地前缀重叠）
    h.replayById['s-1'] = [
      { role: 'user', content: '旧问题' },
      { role: 'user', content: '窗口里发的新消息' },
    ]
    release()
    await sel
    expect(store.messages.map((m) => m.content)).toEqual(['旧问题', '窗口里发的新消息', ''])
  })

  it('切换回已有缓冲的会话不重放覆盖（后台跑过的现场保留）', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('现场消息')
    const id = store.sessionId
    h.replayById[id] = [{ role: 'user', content: '账本里的旧消息' }] // 若误重放会被覆盖
    await store.selectSession(id)
    // 0.2.28：现场 = user + 思考占位（仍是流式空段）
    expect(store.messages.map((m) => m.content)).toEqual(['现场消息', ''])
    expect(store.messages[1].streaming).toBe(true)
  })
})

