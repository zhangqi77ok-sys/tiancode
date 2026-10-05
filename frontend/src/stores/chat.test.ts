import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

const h = vi.hoisted(() => ({
  summaries: [] as { id: string; title: string; workspace?: string; lastActiveMs?: number }[],
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
  // 多会话：按会话 ID 定制重放（缺省回落 h.replay）；seq 是投影的用户消息锚点（0.0.11）
  replayById: {} as Record<string, { role: string; content: string; seq?: number }[]>,
  // 可控延迟：模拟 Replay 的 IPC 往返窗口（窗口内发送的合并测试用）
  replayGate: null as Promise<void> | null,
  resolved: [] as string[],
  sends: [] as string[],
  // 多会话：记录每次 Send 的目标会话（断言后台会话的队列续发归属）
  sendCalls: [] as { sessionID: string; text: string }[],
  // 带附件发送记录（0.0.11：队列续发带附件必须走 SendWithAttachments，而不是纯文本 Send）
  attachCalls: [] as { sessionID: string; text: string; attachments: string }[],
  resolvedAsks: [] as { id: string; answer: string }[],
  // 工作区调用记录（0.0.18：点开有归属的会话，新对话根跟随该归属）
  setWorkspaceCalls: [] as string[],
  // GetWorkspace 的回显值：SetWorkspace 写入后 refresh 能读到（贴近真实后端）
  workspace: '',
  // 可控延迟：指定会话的 ReplayTail 慢半拍（连点切换的竞态测试用）
  slowReplayIds: new Set<string>(),
  // 会话迁移调用记录（0.0.19）
  moveCalls: [] as { id: string; dir: string }[],
  // 任务栏闪烁次数（0.0.19：后台会话终态提示）
  flashCalls: 0,
  // 点链接 → 会话浏览器打开（0.0.29）
  navigations: [] as { sessionID: string; url: string }[],
  failNavigate: false,
  externalOpens: [] as string[],
  planCalls: [] as { sessionID: string; text: string }[],
}))

vi.mock('../wails', () => ({
  openExternal: (url: string) => {
    h.externalOpens.push(url)
  },
  bridge: () => ({
    app: {
      ListSessionSummaries: async () => h.summaries,
      Replay: async (id: string) => {
        if (h.replayGate) await h.replayGate
        return h.replayById[id] ?? h.replay
      },
      // 0.3 尾屏优先：分页投影按同一份数据切片——测试数据都小于一页，
      // 行为与全量 Replay 一致（旧断言不动，契约不变）
      ReplayTail: async (id: string, limit: number) => {
        if (h.replayGate) await h.replayGate
        if (h.slowReplayIds.has(id)) await new Promise((r) => setTimeout(r, 20))
        const all = h.replayById[id] ?? h.replay
        const from = Math.max(0, all.length - limit)
        return { messages: all.slice(from), total: all.length, from }
      },
      ReplayOlder: async (id: string, from: number, limit: number) => {
        const all = h.replayById[id] ?? h.replay
        const start = Math.max(0, from - limit)
        return { messages: all.slice(start, from), total: all.length, from: start }
      },
      SendPlan: async (sessionID: string, text: string) => {
        h.planCalls.push({ sessionID, text })
      },
      Send: async (sessionID: string, text: string) => {
        h.sendCalls.push({ sessionID, text })
        h.sends.push(text)
      },
      SendWithAttachments: async (sessionID: string, text: string, attachments: string) => {
        h.attachCalls.push({ sessionID, text, attachments })
      },
      Stop: async () => {},
      FlashWindow: async () => {
        h.flashCalls++
      },
      MoveSession: async (id: string, dir: string) => {
        h.moveCalls.push({ id, dir })
      },
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
      GetWorkspace: async () => h.workspace,
      SetWorkspace: async (dir: string) => {
        h.setWorkspaceCalls.push(dir)
        h.workspace = dir
      },
      BrowserNavigate: async (sessionID: string, url: string) => {
        if (h.failNavigate) throw new Error('仅支持 http/https 链接')
        h.navigations.push({ sessionID, url })
        return {
          shot: 's-1/shot-0001.png',
          url: 'https://example.com/',
          console: ['[log] hi'],
          output: '[ref=e1] 链接',
          title: 'open example.com',
        }
      },
    },
    runtime: { EventsOn: () => {} },
  }),
}))

const { useChatStore, END_REASON } = await import('./chat')
const { useWorkspaceStore } = await import('./workspace')
const { useDialogs } = await import('../composables/useDialogs')

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
    h.attachCalls = []
    h.resolvedAsks = []
    h.setWorkspaceCalls = []
    h.moveCalls = []
    h.flashCalls = 0
    h.workspace = ''
    h.slowReplayIds = new Set()
  })

  // 0.0.18：点开有归属的会话 = 打算在这个项目干活——"新对话默认根"跟随该归属
  // （侧栏空间高亮、顶栏「当前」标记、"新建对话"落点一起跟）。跟随是**完整**的：
  // 无归属（纯对话/未分组）会话把默认根一并清空，新建对话延续纯对话（实机反馈）；
  // 且只切根不 newSession（跟随 ≠ 抛掉正在看的会话）。
  it('点开有归属的会话：新对话根跟随归属，不 newSession', async () => {
    h.summaries = [{ id: 's-proj', title: 'B 项目', workspace: 'D:/proj-b' }]
    const store = useChatStore()
    await store.loadSessions() // 跟随的数据源是 summaries（账本首个 workspace 事件）
    await store.selectSession('s-proj')
    await new Promise((r) => setTimeout(r, 0)) // 跟随是 fire-and-forget 的 setPath
    expect(h.setWorkspaceCalls).toEqual(['D:/proj-b'])
    expect(store.sessionId).toBe('s-proj') // 视图仍是所点会话
  })

  it('点开无归属会话：默认根一并清空，新建对话延续纯对话（完整跟随）', async () => {
    h.summaries = [{ id: 's-loose', title: '未分组' }]
    const store = useChatStore()
    const ws = useWorkspaceStore()
    await ws.setPath('D:/proj-a') // 当前在工作区 A
    h.setWorkspaceCalls.length = 0
    await store.loadSessions()
    await store.selectSession('s-loose')
    await new Promise((r) => setTimeout(r, 0))
    expect(h.setWorkspaceCalls).toEqual(['']) // 显式清空（顶栏/侧栏高亮同步变化，非暗改）
    expect(ws.path).toBe('')
  })

  it('已是纯对话时点无归属会话：不下发重复清空', async () => {
    h.summaries = [{ id: 's-loose', title: '未分组' }]
    const store = useChatStore()
    await store.loadSessions() // ws.path 本就是 ''
    await store.selectSession('s-loose')
    await new Promise((r) => setTimeout(r, 0))
    expect(h.setWorkspaceCalls).toEqual([])
  })

  // 0.0.21 时间线跳转：目标在缓冲内直接发定位信号；在缓冲外（尾屏分页未载入）
  // 先逐页补载再发信号；n 递增保证重复跳同一轮也触发。
  // 0.0.29 工作区设置修订号：设置面板保存后 bump，派生视图（快捷命令三槽）
  // watch 它重拉——此前配完命令要切一次会话才现形（"配了没效果"）。
  it('bumpWsSettings：修订号递增，供派生视图 watch', () => {
    const store = useChatStore()
    const before = store.wsSettingsRev
    store.bumpWsSettings()
    expect(store.wsSettingsRev).toBe(before + 1)
  })

  it('jumpToSeq：缓冲内直接定位，缓冲外补页后定位', async () => {
    const many: { role: string; content: string; seq?: number }[] = []
    for (let i = 0; i < 200; i++) {
      many.push({ role: i % 2 === 0 ? 'user' : 'assistant', content: `第 ${i} 条`, seq: i % 2 === 0 ? i : undefined })
    }
    h.summaries = [{ id: 's-long', title: '长会话', lastActiveMs: 1 }]
    h.replayById['s-long'] = many
    const store = useChatStore()
    await store.init()
    await store.selectSession('s-long')

    // 尾屏只有 40 条：目标 userSeq=120（第 120 条消息）在缓冲外，补页后命中
    const before = store.jumpSig
    await store.jumpToSeq(120)
    expect(store.messages.length).toBeGreaterThan(40) // 补了页
    expect(store.jumpSig.seq).toBe(120)
    expect(store.jumpSig.n).toBe(before.n + 1)

    // 再跳同一个 seq：n 仍递增（重复跳同一轮也能触发组件 watch）
    const n = store.jumpSig.n
    await store.jumpToSeq(120)
    expect(store.jumpSig.n).toBe(n + 1)
  })

  it('jumpToSeq：翻到头仍找不到时如实提示，不发定位信号', async () => {
    h.summaries = [{ id: 's-short', title: '短会话', lastActiveMs: 1 }]
    h.replayById['s-short'] = [{ role: 'user', content: '只有一条', seq: 5 }]
    const store = useChatStore()
    await store.init()
    await store.selectSession('s-short')

    const before = store.jumpSig
    await store.jumpToSeq(999) // 投影里没有的 seq
    expect(store.jumpSig.n).toBe(before.n) // 信号未发
  })

  it('会话摘要缺失：不碰工作区（无从得知归属）', async () => {
    const store = useChatStore()
    const ws = useWorkspaceStore()
    await ws.setPath('D:/proj-a')
    h.setWorkspaceCalls.length = 0
    await store.selectSession('s-ghost') // 不在 summaries 里
    await new Promise((r) => setTimeout(r, 0))
    expect(h.setWorkspaceCalls).toEqual([])
    expect(ws.path).toBe('D:/proj-a')
  })

  it('点开归属即当前根的会话：不重复下发 SetWorkspace', async () => {
    h.summaries = [{ id: 's-proj', title: 'B 项目', workspace: 'D:/proj-b' }]
    const store = useChatStore()
    const ws = useWorkspaceStore()
    await ws.setPath('D:/proj-b')
    h.setWorkspaceCalls.length = 0
    await store.loadSessions()
    await store.selectSession('s-proj')
    await new Promise((r) => setTimeout(r, 0))
    expect(h.setWorkspaceCalls).toEqual([]) // setPath 对同值短路
  })

  // 0.0.18 修复：启动恢复**最近活跃**的会话——此前取 sessions[0]，而会话列表按
  // ID 升序（os.ReadDir 文件名序），[0] 是最老的一场。启动也不跟随工作区
  // （workspace store 契约：启动不自动进工作区）。
  it('init 选中最近活跃会话，且不跟随工作区', async () => {
    h.summaries = [
      { id: 's-old', title: '老', lastActiveMs: 1000, workspace: 'D:/old' },
      { id: 's-new', title: '新', lastActiveMs: 9000, workspace: 'D:/new' },
    ]
    const store = useChatStore()
    await store.init()
    expect(store.sessionId).toBe('s-new')
    expect(h.setWorkspaceCalls).toEqual([])
  })

  // 0.0.18 竞态修复：翻页锚点按会话各存一份——A 晚到的尾屏不得污染 B 的锚点
  //（旧全局单值下，B 视图会以 A 的下标翻页、错页前插进 B 的缓冲）。
  it('快速连点会话：晚到的尾屏不错写当前视图的翻页锚点', async () => {
    h.replayById = {
      's-a': Array.from({ length: 60 }, (_, i) => ({ role: 'user', content: `a${i}` })),
      's-b': Array.from({ length: 10 }, (_, i) => ({ role: 'user', content: `b${i}` })),
    }
    h.slowReplayIds = new Set(['s-a']) // A 的尾屏慢半拍：B 先就绪
    const store = useChatStore()
    const pA = store.selectSession('s-a')
    await store.selectSession('s-b')
    await pA // A 晚到
    expect(store.sessionId).toBe('s-b')
    expect(store.olderAvailable).toBe(false) // B 只有 10 条；被 A 的 20 污染会误报"有更早的"
    await store.selectSession('s-a')
    expect(store.olderAvailable).toBe(true) // A 自己的锚点（60 - 40）仍在
  })

  // 0.0.19：后台会话终态主动提示（toast + 任务栏闪烁）；当前视图不打扰
  it('后台会话终态：toast 提示 + 任务栏闪烁，当前视图不闪', async () => {
    const { useToast } = await import('../composables/useToast')
    const { toasts } = useToast()
    h.summaries = [{ id: 's-bg', title: '后台活' }]
    const store = useChatStore()
    await store.loadSessions()
    const before = toasts.value.length
    // 后台会话（当前视图是草稿）：完成 → info toast + 闪烁
    store.sessionId = ''
    store.onTerminal({ sessionID: 's-bg', endReason: END_REASON.DONE, error: '' })
    await new Promise((r) => setTimeout(r, 0))
    expect(toasts.value.length).toBe(before + 1)
    expect(toasts.value[toasts.value.length - 1].text).toContain('后台活')
    expect(toasts.value[toasts.value.length - 1].text).toContain('已完成')
    expect(h.flashCalls).toBe(1)
    // 当前视图的终态：不打扰（无新 toast、不闪烁）
    store.sessionId = 's-bg'
    store.onTerminal({ sessionID: 's-bg', endReason: END_REASON.DONE, error: '' })
    await new Promise((r) => setTimeout(r, 0))
    expect(toasts.value.length).toBe(before + 1)
    expect(h.flashCalls).toBe(1)
  })

  // 0.0.19：文件树自动跟手——当前会话的 fs 写/改卡落定 → treeRev +1；后台会话/running 卡不跟
  it('fs 写改卡落定推文件树刷新信号（当前会话限定）', async () => {
    const store = useChatStore()
    store.sessionId = 's-cur'
    const rev0 = store.treeRev
    store.onTool({ sessionID: 's-cur', name: 'fs', status: 'running', summary: 'x', op: 'write' })
    expect(store.treeRev).toBe(rev0) // running 不算落定
    store.onTool({ sessionID: 's-cur', name: 'fs', status: 'success', summary: 'written a.go', op: 'write' })
    expect(store.treeRev).toBe(rev0 + 1)
    store.onTool({ sessionID: 's-bg', name: 'fs', status: 'success', summary: 'written b.go', op: 'edit' })
    expect(store.treeRev).toBe(rev0 + 1) // 后台会话写它自己的工作区，当前树不跟
    store.onTool({ sessionID: 's-cur', name: 'shell', status: 'success', summary: 'ok' })
    expect(store.treeRev).toBe(rev0 + 1) // 非 fs 不跟
  })

  // 0.0.19：迁移会话走后端并刷新摘要
  it('moveSession 走后端并刷新摘要', async () => {
    h.summaries = [{ id: 's-1', title: 'A', workspace: 'D:/old' }]
    const store = useChatStore()
    await store.loadSessions()
    await store.moveSession('s-1', 'D:/new')
    expect(h.moveCalls).toEqual([{ id: 's-1', dir: 'D:/new' }])
    expect(store.summaries[0].workspace).toBe('D:/old') // mock 不改归属；loadSessions 已重跑（调用可见即可）
  })

  // 0.0.18：载入态按会话各记一份——A 先返回的 finally 不熄掉 B 正在转的"载入中"
  it('连点会话：载入态跟随当前视图，不被先完成的会话提前熄掉', async () => {
    h.replayById = {
      's-a': [{ role: 'user', content: 'a1' }],
      's-b': [{ role: 'user', content: 'b1' }],
    }
    h.slowReplayIds = new Set(['s-b']) // B 慢：A 先完成
    const store = useChatStore()
    const pA = store.selectSession('s-a')
    const pB = store.selectSession('s-b')
    await pA
    expect(store.loadingSession).toBe(true) // 当前视图是 B，仍在载入（旧全局布尔此处已是 false）
    await pB
    expect(store.loadingSession).toBe(false)
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

  // 0.0.25：带附件的一轮正常结束却一个字符都没有 → 占位**不删**，写明"模型没有返回内容"。
  // 实机反馈"上传文件或图片后发出去没有回复"：删掉占位就表现为发出去就没了，
  // 用户分不清"模型没答"和"附件根本没送达"。
  it('带附件的空回答保留占位并写明原因', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('看这张图', [
      { kind: 'image', name: 'shot.png', mediaType: 'image/png', size: 3, dataB64: 'AAA', inline: 'full' },
    ])
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.DONE, error: '' })
    const last = store.messages.at(-1)
    expect(last?.role).toBe('assistant')
    expect(last?.content).toBe('模型没有返回内容')
    expect(last?.streaming).toBe(false)
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

  // 0.0.06：running 工具事件先出"执行中"卡，终态事件按 callID 原地更新同一张卡
  //（卡随事件生长，不插两张卡）；无 callID 的终态事件（旧后端/重放）行为不变
  it('running 卡随终态事件原地生长', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onTool({ sessionID: store.sessionId, name: 'shell', status: 'running', summary: '执行中…', callID: 'call-1' })
    let cards = store.messages.filter((m) => m.role === 'tool')
    expect(cards).toHaveLength(1)
    expect(cards[0].status).toBe('running')
    expect(cards[0].streaming).toBe(true)
    // 终态：同 callID → 原地更新
    store.onTool({ sessionID: store.sessionId, name: 'shell', status: 'success', summary: 'done', content: 'output text', callID: 'call-1' })
    cards = store.messages.filter((m) => m.role === 'tool')
    expect(cards).toHaveLength(1)
    expect(cards[0].status).toBe('success')
    expect(cards[0].content).toBe('output text')
    expect(cards[0].streaming).toBe(false)
    // 重复 running 事件不叠加；0.0.07：同 CallID 的过程推送**更新内容**（不吞掉）
    store.onTool({ sessionID: store.sessionId, name: 'shell', status: 'running', summary: '执行中…', content: 'line1', callID: 'call-2' })
    store.onTool({ sessionID: store.sessionId, name: 'shell', status: 'running', summary: '执行中…', content: 'line1\nline2', callID: 'call-2' })
    cards = store.messages.filter((m) => m.role === 'tool')
    expect(cards).toHaveLength(2)
    const shellCard = cards.find((m) => m.callId === 'call-2')
    expect(shellCard?.content).toBe('line1\nline2')
    expect(shellCard?.status).toBe('running')
  })

  // 0.0.07：终态事件携带撤销元数据 → 卡片可显示"恢复写入前"；不可恢复说明透传
  it('终态事件携带撤销元数据与不可恢复说明', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onTool({ sessionID: store.sessionId, name: 'fs', status: 'success', summary: 'written a.go', callID: 'call-u1', hasUndo: true, undoPath: 'a.go' })
    const card = store.messages.find((m) => m.role === 'tool')
    expect(card?.hasUndo).toBe(true)
    expect(card?.undoPath).toBe('a.go')
    // 不可恢复说明（超大文件未保存快照）
    store.onTool({ sessionID: store.sessionId, name: 'fs', status: 'success', summary: 'written big.bin', callID: 'call-u2', undoNote: '这次无法恢复：写入前的内容超过上限，未保存恢复数据' })
    const noteCard = store.messages.filter((m) => m.role === 'tool').at(-1)
    expect(noteCard?.hasUndo).toBeFalsy()
    expect(noteCard?.undoNote).toContain('无法恢复')
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

  // 0.0.11 生命周期：新用户消息 = 新任务开始——旧清单立即退场，不再挂着旧的
  //（修"任务清单一直显示旧的"：模型新一轮不调 todo 工具时旧清单会永远残留）
  it('发送新消息作废旧任务卡，新一轮 onTodo 重新建卡', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('第一个任务')
    store.onTodo({ sessionID: store.sessionId, items: [{ text: '旧步骤', status: 'pending' }] })
    expect(store.messages.some((m) => m.role === 'todo')).toBe(true)

    await store.send('第二个任务') // 旧清单应退场
    expect(store.messages.some((m) => m.role === 'todo')).toBe(false)

    store.onTodo({ sessionID: store.sessionId, items: [{ text: '新步骤', status: 'in_progress' }] })
    const cards = store.messages.filter((m) => m.role === 'todo')
    expect(cards).toHaveLength(1)
    expect(cards[0].todos).toEqual([{ text: '新步骤', status: 'in_progress' }])
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

  // 中断语义：onTerminal 复位 stopping 之前必须快照——此前判 !c.stopping 恒真，
  // "点了中断不续发队列"全靠 stop() 清队列兜着；中断期间重新入队的消息也绝不自动续发
  it('中断确认到达后不自动续发队列（含中断期间重新入队的消息）', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('第一条')
    store.stop() // 用户点中断：置 stopping 并清队列
    expect(store.stopping).toBe(true)
    store.enqueue('中断后重新入队') // stop 之后用户又排了一条
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.DONE, error: '' })
    await new Promise((r) => setTimeout(r, 0))
    expect(h.sends).toEqual(['第一条']) // 中断后的终态不自动续发
    expect(store.queue.map((q) => q.text)).toEqual(['中断后重新入队']) // 留给用户手动发
    expect(store.stopping).toBe(false) // 运行态照常复位
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
    expect(t).toEqual({ text: '三', atts: [] }) // 0.0.11：取回编辑连附件一起返回
    expect(store.queue.map((q) => q.text)).toEqual(['一', '二'])
    store.removeQueued(store.queue[0].id)
    expect(store.queue.map((q) => q.text)).toEqual(['二'])
  })

  // 0.0.11：附件随队列走——running 期间入队带图片，终态后续发必须走
  // SendWithAttachments（此前只排文字，附件留在待发送区，续发消息静默丢附件）
  it('队列消息带附件：续发走 SendWithAttachments', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('第一条')
    const img = {
      kind: 'image' as const,
      name: 'shot.png',
      mediaType: 'image/png',
      size: 3,
      dataB64: 'AAA',
      inline: 'none' as const,
    }
    store.enqueue('带图的第二条', [img])
    store.onTerminal({ sessionID: store.sessionId, endReason: END_REASON.DONE, error: '' })
    await new Promise((r) => setTimeout(r, 0))
    expect(h.attachCalls.at(-1)).toMatchObject({ sessionID: store.sessionId, text: '带图的第二条' })
    expect(h.attachCalls.at(-1)?.attachments).toContain('shot.png')
    expect(h.sends).toEqual(['第一条']) // 续发不走纯文本 Send
    expect(store.queue).toEqual([])
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

  // 0.0.21：后台会话等审批/答复必须穿透当前焦点被看见——toast（带点击跳转动作）
  // + 任务栏闪烁；当前会话的卡片已在眼前，不打扰。
  it('后台审批/问答到达：toast 带跳转动作 + 任务栏闪烁；当前会话不闪', async () => {
    const { useToast } = await import('../composables/useToast')
    const toastsOf = () => useToast().toasts.value
    const store = useChatStore()
    await store.newSession()
    await store.send('A')
    const a = store.sessionId
    await store.newSession()
    await store.send('B')
    const before = h.flashCalls

    // 后台会话 a 等审批：toast 入队（文案含会话名与工具名）、FlashWindow +1
    store.onApproval({ id: 'ap-21', sessionID: a, toolName: 'shell', arguments: '{}' })
    const t = toastsOf().at(-1)
    expect(t?.text).toContain('等你确认 shell')
    expect(typeof t?.action).toBe('function') // 点击跳转动作
    expect(h.flashCalls).toBe(before + 1)

    // 点击动作 → 跳到 a
    t?.action?.()
    expect(store.sessionId).toBe(a)

    // 后台问答：同款提示
    await store.selectSession('') // 离开 a，模拟用户在别处
    await store.newSession()
    const before2 = h.flashCalls
    store.onAsk({ id: 'ask-21', sessionID: a, question: '选哪个？' })
    expect(toastsOf().at(-1)?.text).toContain('等你回答')
    expect(h.flashCalls).toBe(before2 + 1)

    // 当前会话的审批：卡片已在眼前，不 toast 不闪烁
    await store.selectSession(store.sessionId)
    await store.send('再问一次')
    const cur = store.sessionId
    const before3 = h.flashCalls
    store.onApproval({ id: 'ap-22', sessionID: cur, toolName: 'fs', arguments: '{}' })
    expect(h.flashCalls).toBe(before3) // 不闪烁
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

// 浏览器驾驶舱（0.0.28）：browser 工具的 shot/url/console 随工具卡进会话缓冲，
// 面板数据源 = 当前会话缓冲的派生（切会话跟随、后台事件不串台、Replay 同构恢复）。
describe('chat store · 浏览器驾驶舱', () => {
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
    h.attachCalls = []
    h.resolvedAsks = []
    h.setWorkspaceCalls = []
    h.navigations = []
    h.failNavigate = false
    h.externalOpens = []
  })

  function browserTool(over: Record<string, unknown>) {
    return { sessionID: '', name: 'browser', status: 'success', summary: 'x', ...over }
  }

  it('browser 终态卡携带 shot/url/console 进会话缓冲', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('打开页面看看')
    store.onTool(
      browserTool({
        sessionID: store.sessionId,
        callID: 'b1',
        title: 'open http://localhost:5173',
        shot: 's-1/shot-0001.png',
        url: 'http://localhost:5173/',
        console: ['10:00:00 [log] ready'],
      }),
    )
    const card = store.messages[store.messages.length - 1]
    expect(card.toolName).toBe('browser')
    expect(card.shot).toBe('s-1/shot-0001.png')
    expect(card.url).toBe('http://localhost:5173/')
    expect(card.console).toEqual(['10:00:00 [log] ready'])
    expect(store.browserVisual).toMatchObject({
      shot: 's-1/shot-0001.png',
      url: 'http://localhost:5173/',
      console: ['10:00:00 [log] ready'],
      running: false,
    })
  })

  it('running 卡原地更新为终态后补上驾驶舱字段（running 卡不带字段）', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('点一下')
    store.onTool(browserTool({ sessionID: store.sessionId, status: 'running', callID: 'b2', summary: '执行中…' }))
    expect(store.messages[store.messages.length - 1].shot).toBeUndefined()
    expect(store.browserVisual.running).toBe(true)
    store.onTool(
      browserTool({
        sessionID: store.sessionId,
        callID: 'b2',
        title: 'click [3]',
        shot: 's-1/shot-0002.png',
        url: 'http://localhost:5173/',
        console: [],
      }),
    )
    // 同 callID 原地生长：不插第二张卡
    const cards = store.messages.filter((m) => m.role === 'tool')
    expect(cards).toHaveLength(1)
    expect(cards[0].status).toBe('success')
    expect(cards[0].shot).toBe('s-1/shot-0002.png')
    expect(store.browserVisual.running).toBe(false)
  })

  it('browserVisual 各字段取最新携带者的值；截图失败（shot 为空串）不回退旧图', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('连续动作')
    store.onTool(
      browserTool({
        sessionID: store.sessionId,
        callID: 'b1',
        title: 'open http://a.dev',
        content: '已打开 http://a.dev/\n\n[0] button 登录',
        shot: 's-1/shot-0001.png',
        url: 'http://a.dev/',
        console: ['10:00:00 [log] a'],
      }),
    )
    // 第二次动作截图失败：shot=""（明确携带的空值，照实显示）、console 无输出（[]）
    store.onTool(
      browserTool({ sessionID: store.sessionId, callID: 'b2', title: 'click [1]', shot: '', url: 'http://a.dev/#x' }),
    )
    expect(store.browserVisual.shot).toBe('')
    expect(store.browserVisual.url).toBe('http://a.dev/#x')
    expect(store.browserVisual.console).toEqual(['10:00:00 [log] a'])
    // 元素快照取最近一次 open/snapshot/scroll 卡的正文，click 卡不算
    expect(store.browserVisual.snapshot).toContain('已打开')
  })

  it('当前会话的 browser 卡到达：自动打开右栏并切到浏览器 tab', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('开浏览器')
    expect(store.browserOpen).toBe(false)
    store.onTool(browserTool({ sessionID: store.sessionId, status: 'running', callID: 'b1', summary: '执行中…' }))
    expect(store.browserOpen).toBe(true)
    expect(store.rightPanelTab).toBe('browser')
  })

  it('后台会话的 browser 事件入自己的缓冲，不抢当前视图的浏览器 tab', async () => {
    h.summaries = [{ id: 's-a', title: 'A' }, { id: 's-b', title: 'B' }]
    const store = useChatStore()
    await store.selectSession('s-a')
    store.onTool(
      browserTool({
        sessionID: 's-b',
        callID: 'b1',
        title: 'open http://b.dev',
        shot: 's-b/shot-0001.png',
        url: 'http://b.dev/',
      }),
    )
    expect(store.browserOpen).toBe(false) // 不抢当前视图
    await store.selectSession('s-b')
    // 切过去面板跟随当前会话：派生数据来自 s-b 自己的缓冲
    expect(store.browserVisual).toMatchObject({ shot: 's-b/shot-0001.png', url: 'http://b.dev/' })
  })

  it('Replay 同构恢复 shot/url/console（重启后卡片不丢）', async () => {
    h.summaries = [{ id: 's-1', title: '历史' }]
    h.replayById['s-1'] = [
      { role: 'user', content: '看看页面' },
      {
        role: 'tool',
        content: '已打开 http://localhost:5173',
        toolName: 'browser',
        status: 'success',
        title: 'open http://localhost:5173',
        shot: 's-1/shot-0001.png',
        url: 'http://localhost:5173/',
        console: ['10:00:00 [error] boom'],
      } as never,
    ]
    const store = useChatStore()
    await store.selectSession('s-1')
    expect(store.browserVisual).toMatchObject({
      shot: 's-1/shot-0001.png',
      url: 'http://localhost:5173/',
      console: ['10:00:00 [error] boom'],
    })
    expect(store.browserVisual.snapshot).toContain('已打开')
  })

  it('非 browser 工具不携带驾驶舱字段，browserVisual 保持空态', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('列个目录')
    store.onTool({ sessionID: store.sessionId, name: 'fs', status: 'success', summary: 'ls', callID: 'f1' })
    expect(store.messages[store.messages.length - 1].shot).toBeUndefined()
    expect(store.browserVisual).toEqual({ shot: '', url: '', console: [], snapshot: '', running: false })
  })

  // 点链接 → 右侧驾驶舱（0.0.29）：链接绝不许把应用窗口本身导航走（WebView 无
  // 地址栏无后退，进去就被困住）。成功 = 会话浏览器打开 + 合成与模型工具卡同构
  // 的本地卡（面板数据源唯一：会话缓冲派生）；失败 = 回退系统浏览器。
  it('openLinkInBrowser 成功：合成 browser 本地卡并打开浏览器 tab', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.openLinkInBrowser('https://example.com')
    expect(h.navigations).toEqual([{ sessionID: store.sessionId, url: 'https://example.com' }])
    const card = store.messages.find((m) => m.role === 'tool' && m.toolName === 'browser')
    expect(card).toMatchObject({
      shot: 's-1/shot-0001.png',
      url: 'https://example.com/',
      title: 'open example.com',
      status: 'success',
    })
    // title 以动作名开头（与模型工具卡同构）→ 面板元素快照可见
    expect(store.browserVisual).toMatchObject({ url: 'https://example.com/' })
    expect(store.browserVisual.snapshot).toContain('[ref=e1]')
    expect(store.browserOpen).toBe(true)
    expect(store.rightPanelTab).toBe('browser')
  })

  it('openLinkInBrowser 失败：回退系统浏览器，不占对话窗口也不留半截卡', async () => {
    h.failNavigate = true
    const store = useChatStore()
    await store.newSession()
    await store.openLinkInBrowser('https://example.com')
    expect(h.externalOpens).toEqual(['https://example.com'])
    expect(h.navigations).toEqual([])
    expect(store.messages.some((m) => m.role === 'tool')).toBe(false)
    expect(store.browserOpen).toBe(false)
  })

  // 检查自愈（0.0.34）：系统发起的修复回合——置 running 态并落可见说明气泡；
  // 终态由既有 onTerminal 复位（这里一并锁住闭环）。
  it('onAutoFix：置 running + 说明气泡可见；onTerminal 后解锁', () => {
    const store = useChatStore()
    void store.newSession()
    const sid = store.sessionId
    store.onAutoFix({
      sessionID: sid,
      reason: '（系统）工作区检查未通过（go test ./... 报告 2 处问题），自动定向修复中。',
      attempt: 1,
      max: 2,
    })
    const convo = store.messages
    expect(store.running).toBe(true)
    expect(convo.some((m) => m.role === 'assistant' && m.content.includes('自动定向修复'))).toBe(true)
    store.onTerminal({ sessionID: sid, endReason: END_REASON.DONE, error: '' })
    expect(store.running).toBe(false)
  })

  // 方案模式（0.0.35）：发送走 SendPlan 且一次性关闭；方案回合终态后弹确认卡，
  // 确认 = 方案文本随**普通消息**发出（执行回合写路径恢复在场），取消 = 什么都不发生。
  it('方案模式：SendPlan 一次性；终态后确认卡，执行随普通消息', async () => {
    const store = useChatStore()
    await store.newSession()
    store.planMode = true
    await store.send('给登录加上记住我')
    const sid = store.sessionId // 草稿首聊：ID 在发送那一刻才领取
    expect(h.planCalls[0].sessionID).toBe(sid)
    expect(h.planCalls[0].text).toBe('给登录加上记住我')
    expect(h.sends).toEqual([])
    expect(store.planMode).toBe(false)
    store.onChunk({ sessionID: sid, delta: '方案：1. 给登录页加"记住我"复选框 2. 跑 pnpm test 验证', thinking: '' })
    store.onTerminal({ sessionID: sid, endReason: END_REASON.DONE, error: '' })
    const { confirmState, resolveConfirm } = useDialogs()
    expect(confirmState.value?.title).toBe('实施方案确认')
    expect(confirmState.value?.message).toContain('记住我')
    resolveConfirm(true)
    await new Promise((r) => setTimeout(r, 0))
    expect(h.sends.at(-1)).toContain('按以下方案执行')
    expect(h.planCalls.length).toBe(1) // 执行回合不再走 SendPlan
  })

  it('方案模式：确认卡取消则什么都不发', async () => {
    const store = useChatStore()
    await store.newSession()
    const sid = store.sessionId
    store.planMode = true
    await store.send('先出方案')
    store.onChunk({ sessionID: sid, delta: '方案……', thinking: '' })
    store.onTerminal({ sessionID: sid, endReason: END_REASON.DONE, error: '' })
    const { confirmState, resolveConfirm } = useDialogs()
    resolveConfirm(false)
    await new Promise((r) => setTimeout(r, 0))
    expect(h.sends.filter((t) => t.includes('按以下方案执行'))).toEqual([])
  })
})


// 右栏新 tab（目录/任务）：开态自持 + open 内置激活切换（与 openBrowserPanel 同一不变式），
// 任务面板在当前会话的 bg_start 卡到达时自动打开（驾驶舱同一联动纪律）。
describe('chat store · 右栏「目录」「任务」tab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    h.summaries = []
    h.replay = []
  })

  it('开态自持：open 置开态并切激活 tab，close 只关自己（激活 tab 不动，容器自动回退）', () => {
    const store = useChatStore()
    store.openTreePanel()
    expect(store.treeOpen).toBe(true)
    expect(store.rightPanelTab).toBe('tree')
    store.closeTreePanel()
    expect(store.treeOpen).toBe(false)
    expect(store.rightPanelTab).toBe('tree') // 收起不动激活：注册表缩回后由容器回退第一项

    store.openTasksPanel()
    expect(store.tasksOpen).toBe(true)
    expect(store.rightPanelTab).toBe('tasks')
    store.closeTasksPanel()
    expect(store.tasksOpen).toBe(false)
  })

  it('当前会话的 bg_start 卡（内容 JSON 带 task_id+pid）自动打开任务面板并切激活', async () => {
    const store = useChatStore()
    await store.newSession()
    expect(store.tasksOpen).toBe(false)
    store.onTool({
      sessionID: store.sessionId,
      name: 'shell',
      status: 'success',
      summary: 'x',
      callID: 'bg-card-1',
      content: '{"task_id":"bg-1","pid":42,"status":"running"}',
    })
    expect(store.tasksOpen).toBe(true)
    expect(store.rightPanelTab).toBe('tasks')
  })

  it('普通 shell 输出、后台会话的 bg_start 卡不打开面板（不抢当前视图）', async () => {
    const store = useChatStore()
    await store.newSession()
    // run 的普通文本输出不是后台任务卡
    store.onTool({ sessionID: store.sessionId, name: 'shell', status: 'success', summary: 'x', callID: 'r1', content: 'build ok (exit 0)' })
    expect(store.tasksOpen).toBe(false)
    // 后台会话的 bg_start 卡只进它自己的缓冲，不抢当前视图
    store.onTool({ sessionID: 's-other', name: 'shell', status: 'success', summary: 'x', callID: 'b2', content: '{"task_id":"bg-1","pid":42,"status":"running"}' })
    expect(store.tasksOpen).toBe(false)
    // running 卡（过程推送）也不触发
    store.onTool({ sessionID: store.sessionId, name: 'shell', status: 'running', summary: '执行中…', callID: 'r2' })
    expect(store.tasksOpen).toBe(false)
  })
})
