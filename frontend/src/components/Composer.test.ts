import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, ref } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import Composer from './Composer.vue'
import DialogHost from './DialogHost.vue'
import { useChatStore, type PendingAttachment } from '../stores/chat'
import { useCatalogStore } from '../stores/catalog'
import { useChannelStore } from '../stores/channels'
import { consumeEsc } from '../composables/useEsc'

// 第 7 批：重跑 = 回填 → 用户改字 → 按发送时确认分叉（RerunFrom）→ 发送**输入框里的文字**；
// 取消确认时什么都不撤回。这里用真实 DialogHost 驱动确认框（点它的按钮），不 mock 对话框。

const h2 = vi.hoisted(() => ({
  sends: [] as string[],
  modelSends: [] as { text: string; model: string }[],
  sendFails: false,
  activeModelCalls: [] as { channelId: string; model: string }[],
  channels: {
    channels: [
      { id: 'ch1', name: '官方渠道', protocol: 'openai', baseUrl: '', model: 'gpt-4o', apiKey: 'k1', models: ['gpt-4o', 'gpt-4o-mini'] },
      { id: 'ch2', name: 'Claude渠道', protocol: 'anthropic', baseUrl: '', model: 'claude-3-5-sonnet', apiKey: 'k2', models: ['claude-3-5-sonnet'] },
    ],
    activeId: 'ch1',
  },
  attach: [] as { text: string; attachments: string; forceTool: string }[],
  rerunCalls: [] as { seq: number }[],
  probes: [] as string[],
  // @ 文件引用搜索（seq 守卫测试）：记录查询词 + 可控延迟/结果
  atSearches: [] as string[],
  atGate: null as Promise<void> | null,
  atResult: [] as string[],
  // 工作区快捷命令（0.0.26）：null = 未配置
  quickCommands: null as { build: string; test: string; run: string } | null,
  // 会话 Token 用量（0.0.45 任务 10）
  sessionUsage: null as any,
  sessionUsageCalls: [] as string[],
  eventCallbacks: {} as Record<string, ((p?: any) => void)[]>,
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ListChannels: async () => JSON.parse(JSON.stringify(h2.channels)),
      SetActiveModel: async (channelId: string, model: string) => {
        h2.activeModelCalls.push({ channelId, model })
        h2.channels.activeId = channelId
        const ch = h2.channels.channels.find((c) => c.id === channelId)
        if (ch) ch.model = model
      },
      Send: async (_sid: string, text: string) => {
        if (h2.sendFails) throw new Error('发送失败')
        h2.sends.push(text)
      },
      SendWithModel: async (_sid: string, text: string, model: string) => {
        if (h2.sendFails) throw new Error('发送失败')
        h2.modelSends.push({ text, model })
      },
      SendWithAttachments: async (_sid: string, text: string, attachments: string, forceTool: string) => {
        h2.attach.push({ text, attachments, forceTool })
      },
      RerunFrom: async (_sid: string, seq: number) => {
        h2.rerunCalls.push({ seq })
        return { text: '原文', reverted: ['a.go'], skipped: [] }
      },
      // 第 8 批：/ 菜单选中服务器后当场列工具（只读探测，不写账本、不发消息）
      ProbeMcpServer: async (name: string) => {
        h2.probes.push(name)
        if (name === 'remote') {
          throw new Error('远程服务器不支持自动列工具（http://127.0.0.1:8/mcp），请直接指定 tool 调用')
        }
        return ['create_issue', 'list_issues']
      },
      // @ 文件引用搜索：gate 挂起（模拟慢响应），atResult 控制返回
      SearchWorkspaceFiles: async (_sid: string, query: string) => {
        h2.atSearches.push(query)
        if (h2.atGate) await h2.atGate
        return h2.atResult
      },
      QuickCommands: async (_sid: string) => h2.quickCommands,
      RunQuickCommand: async (_sid: string, slot: string) => ({ isError: false, output: `${slot} ok` }),
      SessionUsage: async (sid: string) => {
        h2.sessionUsageCalls.push(sid)
        return h2.sessionUsage
      },
      Stop: async () => {},
    },
    runtime: {
      EventsOn: (event: string, cb: (p?: any) => void) => {
        if (!h2.eventCallbacks[event]) h2.eventCallbacks[event] = []
        h2.eventCallbacks[event].push(cb)
        return () => {
          h2.eventCallbacks[event] = h2.eventCallbacks[event].filter((f) => f !== cb)
        }
      },
    },
  }),
  winClose: async () => {},
  winMinimize: async () => {},
  winToggleMaximize: async () => {},
}))

let host: HTMLElement
let app: ReturnType<typeof createApp> | null = null
const draftRef = ref('原文')
const rerunRef = ref<{ seq: number; attachments: PendingAttachment[] } | null>(null)

function mountComposer(
  initial = '原文',
  rerun: { seq: number; attachments: PendingAttachment[] } | null = { seq: 7, attachments: [] },
) {
  draftRef.value = initial
  rerunRef.value = rerun
  host = document.createElement('div')
  document.body.appendChild(host)
  const pinia = createPinia()
  app = createApp(
    defineComponent({
      render: () =>
        h('div', [
          h(Composer, {
            modelValue: draftRef.value,
            'onUpdate:modelValue': (v: string) => {
              draftRef.value = v
            },
            rerun: rerunRef.value,
          }),
          h(DialogHost),
        ]),
    }),
  )
  app.use(pinia)
  setActivePinia(pinia)
  app.mount(host)
  return host
}

function teardown() {
  app?.unmount()
  app = null
  host?.remove()
  document.body.innerHTML = ''
}

// 技能/MCP 清单：只列已启用项（未启用的必须不出现）
function seedCatalog() {
  const cat = useCatalogStore()
  cat.skills = [
    { id: 's1', name: 'deploy', description: '部署流程', body: '', enabled: true },
    { id: 's2', name: '停用的技能', description: '', body: '', enabled: false },
  ]
  cat.mcp = [
    {
      id: 'm1', name: 'github', transport: 'stdio', command: 'npx', args: '-y gh-mcp', env: '',
      url: '', headers: '', enabled: true,
    },
    {
      id: 'm3', name: 'remote', transport: 'http', command: '', args: '', env: '',
      url: 'http://127.0.0.1:8/mcp', headers: '', enabled: true,
    },
    {
      id: 'm2', name: '停用的 MCP', transport: 'http', command: '', args: '', env: '',
      url: 'http://127.0.0.1:8/mcp', headers: '', enabled: false,
    },
  ]
}

function clickButton(text: string): boolean {
  const btn = Array.from(document.querySelectorAll('button')).find((b) => (b.textContent ?? '').trim() === text)
  if (!btn) return false
  ;(btn as HTMLButtonElement).click()
  return true
}

describe('Composer（第 7 批：重跑走输入框）', () => {
  beforeEach(() => {
    teardown()
    h2.sends = []
    h2.modelSends = []
    h2.sendFails = false
    h2.activeModelCalls = []
    h2.channels = {
      channels: [
        { id: 'ch1', name: '官方渠道', protocol: 'openai', baseUrl: '', model: 'gpt-4o', apiKey: 'k1', models: ['gpt-4o', 'gpt-4o-mini'] },
        { id: 'ch2', name: 'Claude渠道', protocol: 'anthropic', baseUrl: '', model: 'claude-3-5-sonnet', apiKey: 'k2', models: ['claude-3-5-sonnet'] },
      ],
      activeId: 'ch1',
    }
    h2.attach = []
    h2.rerunCalls = []
    h2.probes = []
    h2.atSearches = []
    h2.atGate = null
    h2.atResult = []
    h2.sessionUsage = null
    h2.sessionUsageCalls = []
    h2.eventCallbacks = {}
  })

  it('确认后发送输入框里改过的文字（不是原文）', async () => {
    mountComposer('原文')
    draftRef.value = '改过的文字'
    await nextTick()
    // 点发送（图标按钮，用 aria-label）→ 弹确认框；分叉发生在确认之后
    const send = document.querySelector('button[aria-label="发送"]') as HTMLButtonElement
    expect(send).toBeTruthy()
    send.click()
    await nextTick()
    expect(clickButton('重跑')).toBe(true) // 确认框里点「重跑」
    await new Promise((r) => setTimeout(r, 0))

    expect(h2.rerunCalls).toEqual([{ seq: 7 }])
    expect(h2.sends).toEqual(['改过的文字'])
  })

  it('取消确认：不撤回、不发送，草稿留着', async () => {
    mountComposer('原文')
    draftRef.value = '改过的文字'
    await nextTick()
    ;(document.querySelector('button[aria-label="发送"]') as HTMLButtonElement).click()
    await nextTick()
    expect(clickButton('取消')).toBe(true)
    await new Promise((r) => setTimeout(r, 0))

    expect(h2.rerunCalls).toEqual([])
    expect(h2.sends).toEqual([])
    expect(draftRef.value).toBe('改过的文字')
  })

  // 第 7 批：/ 选技能 → 发送时把"本轮先调 skill"作为参数下发（不是改写系统提示）
  it('/ 选技能后发送，带上强制工具参数', async () => {
    mountComposer('', null)
    seedCatalog()
    draftRef.value = '/'
    await nextTick()
    ;(document.querySelector('textarea') as HTMLTextAreaElement).dispatchEvent(new Event('keyup'))
    await nextTick()
    const menu = document.querySelector('[aria-label="指定技能或 MCP"]')
    expect(menu).toBeTruthy()
    // 只列已启用项
    expect(menu!.textContent).toContain('deploy')
    expect(menu!.textContent).toContain('github')
    expect(menu!.textContent).not.toContain('停用的')

    const skillBtn = Array.from(menu!.querySelectorAll('button')).find((b) => b.textContent?.includes('deploy'))
    ;(skillBtn as HTMLButtonElement).click()
    await nextTick()
    expect(draftRef.value).toBe('') // 命令词从正文里去掉，选择收成 chip
    expect(document.querySelector('[aria-label="本轮指定的工具"]')?.textContent).toContain('技能 deploy')

    draftRef.value = '帮我部署'
    await nextTick()
    ;(document.querySelector('button[aria-label="发送"]') as HTMLButtonElement).click()
    await new Promise((r) => setTimeout(r, 0))
    expect(h2.attach).toHaveLength(1)
    expect(h2.attach[0].text).toBe('帮我部署')
    expect(h2.attach[0].forceTool).toContain('"name":"skill"')
    expect(h2.attach[0].forceTool).toContain('"deploy"')
  })

  // 第 8 批：选中服务器后**当场**列出它的工具（ProbeServer），不花一轮 tool=list、
  // 不发任何消息；点工具名即绑定 server/tool，参数由模型填（菜单不再代填 {}）
  it('/ 选 MCP：当场列出工具并绑定，参数不代填', async () => {
    mountComposer('', null)
    seedCatalog()
    draftRef.value = '/git'
    await nextTick()
    ;(document.querySelector('textarea') as HTMLTextAreaElement).dispatchEvent(new Event('keyup'))
    await nextTick()
    const menu = document.querySelector('[aria-label="指定技能或 MCP"]') as HTMLElement
    ;(Array.from(menu.querySelectorAll('button')).find((b) => b.textContent?.includes('github')) as HTMLButtonElement).click()
    await new Promise((r) => setTimeout(r, 0)) // 等探测返回
    await nextTick()

    expect(h2.probes).toEqual(['github']) // 探测走的是 ProbeServer 绑定
    expect(h2.sends).toEqual([]) // 不是一次对话发送
    expect(h2.attach).toEqual([])

    const item = Array.from(document.querySelectorAll('[aria-label="指定技能或 MCP"] button')).find((b) =>
      b.textContent?.includes('create_issue'),
    )
    expect(item, '工具名应出现在菜单里').toBeTruthy()
    ;(item as HTMLButtonElement).click()
    await nextTick()
    expect(document.querySelector('[aria-label="本轮指定的工具"]')?.textContent).toContain('MCP github · create_issue')

    draftRef.value = '建个 issue'
    await nextTick()
    ;(document.querySelector('button[aria-label="发送"]') as HTMLButtonElement).click()
    await new Promise((r) => setTimeout(r, 0))
    expect(h2.attach).toHaveLength(1)
    const dto = JSON.parse(h2.attach[0].forceTool) as { name: string; arguments: Record<string, unknown> }
    expect(dto.name).toBe('mcp')
    // 只钉 server/tool：没有嵌套的 arguments —— 参数由模型那次调用提供，菜单不代填
    expect(dto.arguments).toEqual({ server: 'github', tool: 'create_issue' })
  })

  // 第 8 批：远程服务器不支持自动列工具——显示现成错误原文，保留手打工具名
  it('/ 选 MCP 远程服务器：显示不支持原文并可手打', async () => {
    mountComposer('', null)
    seedCatalog()
    draftRef.value = '/remote'
    await nextTick()
    ;(document.querySelector('textarea') as HTMLTextAreaElement).dispatchEvent(new Event('keyup'))
    await nextTick()
    const menu = document.querySelector('[aria-label="指定技能或 MCP"]') as HTMLElement
    ;(Array.from(menu.querySelectorAll('button')).find((b) => b.textContent?.includes('remote')) as HTMLButtonElement).click()
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    const text = (document.querySelector('[aria-label="指定技能或 MCP"]') as HTMLElement).textContent ?? ''
    expect(text).toContain('远程服务器不支持自动列工具') // 现成错误原文，不另编
    const manual = document.querySelector('input[aria-label="MCP 工具名"]') as HTMLInputElement
    expect(manual, '手打工具名必须保留').toBeTruthy()
    manual.value = 'create_issue'
    manual.dispatchEvent(new Event('input'))
    await nextTick()
    ;(Array.from(document.querySelectorAll('[aria-label="指定技能或 MCP"] button')).find((b) =>
      b.textContent?.includes('确定'),
    ) as HTMLButtonElement).click()
    await nextTick()
    expect(document.querySelector('[aria-label="本轮指定的工具"]')?.textContent).toContain('MCP remote · create_issue')
  })

  // 第 8 批：「放进输入框」必须 mousedown.prevent——不拦默认行为的话，按下鼠标先清掉
  // 选区 → selectionchange 把 selectionText 置空 → 按钮在 click 之前被拆掉（点了没反应）
  it('放进输入框：mousedown 不丢选区，click 追加到草稿末尾', async () => {
    mountComposer('已有内容', null)
    const conv = document.createElement('div')
    conv.setAttribute('data-conversation', '')
    conv.textContent = 'foo.go:12'
    document.body.appendChild(conv)
    // jsdom 的 Selection 太薄（toString 恒为空），按组件用到的契约桩一个
    const fake = {
      isCollapsed: false,
      toString: () => 'foo.go:12',
      anchorNode: conv.firstChild,
    } as unknown as Selection
    const spy = vi.spyOn(document, 'getSelection').mockReturnValue(fake)

    document.dispatchEvent(new Event('selectionchange'))
    await nextTick()
    const btn = Array.from(document.querySelectorAll('button')).find((b) =>
      b.textContent?.includes('放进输入框'),
    ) as HTMLButtonElement
    expect(btn).toBeTruthy()

    const down = new MouseEvent('mousedown', { bubbles: true, cancelable: true })
    btn.dispatchEvent(down)
    expect(down.defaultPrevented).toBe(true) // 默认行为（清选区）被拦下
    await nextTick()
    expect(document.querySelector('button[title^="把选中的文字"]')).toBeTruthy() // 按钮没被拆掉

    btn.click()
    await nextTick()
    expect(draftRef.value).toBe('已有内容\n\nfoo.go:12') // 只追加、不替换
    spy.mockRestore()
  })

  // 第 7 批：队列行上的按钮是「提前」（顶到队首，本轮结束才发），文案与图标都不表示"立即发送"
  it('队列「提前」按钮：文案明确，行为仍是置顶', async () => {
    mountComposer('', null)
    const store = useChatStore()
    store.enqueue('一')
    store.enqueue('二')
    await nextTick()
    expect(document.querySelector('button[aria-label="置顶该消息"]')).toBeNull()
    const promotes = Array.from(document.querySelectorAll('button[aria-label="提前：本轮结束后最先发出"]'))
    expect(promotes).toHaveLength(2)
    ;(promotes[1] as HTMLButtonElement).click()
    await nextTick()
    expect(store.queue.map((q) => q.text)).toEqual(['二', '一'])
  })

  it('重跑带回附件时走 SendWithAttachments', async () => {
    mountComposer('原文')
    rerunRef.value = {
      seq: 7,
      attachments: [
        { kind: 'image', name: 'shot.png', mediaType: 'image/png', size: 3, dataB64: 'AAA', inline: 'none' },
      ],
    }
    await nextTick()
    ;(document.querySelector('button[aria-label="发送"]') as HTMLButtonElement).click()
    await nextTick()
    expect(clickButton('重跑')).toBe(true)
    await new Promise((r) => setTimeout(r, 0))

    expect(h2.attach).toHaveLength(1)
    expect(h2.attach[0].text).toBe('原文')
    expect(h2.attach[0].attachments).toContain('shot.png')
    expect(h2.sends).toEqual([])
  })

  // @ 文件引用 seq 守卫：旧关键词的慢响应返回时必须丢弃——否则用户已改词，
  // 弹出的却是上一个关键词的命中（菜单内容与输入不符）
  it('@ 搜索：旧关键词的慢响应不覆盖新菜单', async () => {
    vi.useFakeTimers()
    try {
      mountComposer('', null)
      const ta = document.querySelector('textarea') as HTMLTextAreaElement
      expect(ta).toBeTruthy()

      async function type(text: string) {
        draftRef.value = text
        await nextTick()
        ta.selectionStart = text.length
        ta.dispatchEvent(new Event('input'))
        await nextTick()
      }
      const popupText = () =>
        (document.querySelector('[aria-label="引用工作区文件"]')?.textContent ?? '')

      // 第一代：@ma 的搜索被 gate 挂起（慢响应）
      let release!: () => void
      h2.atGate = new Promise<void>((r) => (release = r))
      await type('@ma')
      await vi.advanceTimersByTimeAsync(150)
      expect(h2.atSearches).toEqual(['ma'])

      // 用户改词：@app 的搜索立即返回新命中
      h2.atGate = null
      h2.atResult = ['app/main.go']
      await type('@app')
      await vi.advanceTimersByTimeAsync(150)
      expect(h2.atSearches).toEqual(['ma', 'app'])
      expect(popupText()).toContain('app/main.go')

      // 旧关键词的响应此刻才返回：必须被丢弃
      h2.atResult = ['old/ma.go']
      release()
      await vi.advanceTimersByTimeAsync(0)
      await nextTick()
      expect(popupText()).toContain('app/main.go')
      expect(popupText()).not.toContain('old/ma.go')
    } finally {
      vi.useRealTimers()
    }
  })

  // 卸载清理：挂起的 @ 防抖定时器必须随组件销毁清掉——回调会写已脱离视图的菜单状态
  it('@ 防抖：卸载后挂起的搜索不再触发', async () => {
    vi.useFakeTimers()
    try {
      mountComposer('', null)
      const ta = document.querySelector('textarea') as HTMLTextAreaElement
      draftRef.value = '@ma'
      await nextTick()
      ta.selectionStart = 3
      ta.dispatchEvent(new Event('input'))
      await nextTick()
      await vi.advanceTimersByTimeAsync(100) // 未到 150ms：搜索还没发出
      teardown()
      await vi.advanceTimersByTimeAsync(200) // 越过防抖窗口
      expect(h2.atSearches).toEqual([])
    } finally {
      vi.useRealTimers()
    }
  })

  // 0.0.33 用户裁决：手动命令行没必要——模型跑命令走同一条审批链，重复入口只添杂乱。
  // 锁两件事：手动输入框/运行按钮不复存在；快捷命令配置了才出现按钮（未配置整行消失）。
  it('命令行：手动输入已移除；快捷命令按钮仅配置后出现', async () => {
    mountComposer('', null)
    await nextTick()
    expect(host.querySelector('input[aria-label="命令行"]')).toBeNull()
    expect(host.querySelector('[title="执行命令（结果进对话区）"]')).toBeNull()
    expect(Array.from(host.querySelectorAll("button")).some((b) => b.textContent?.trim() === 'build')).toBe(false)
    teardown()
    h2.quickCommands = { build: 'go build ./...', test: '', run: '' }
    try {
      mountComposer('', null)
      // loadQuick 是 onMounted 里的异步拉取：轮询等它渲染（单次 nextTick 可能早于 promise 续延）
      await vi.waitFor(() => {
        const chip = Array.from(host.querySelectorAll("button")).find((b) => b.textContent?.trim() === 'build')
        expect(chip).toBeTruthy()
      })
      const chip = Array.from(host.querySelectorAll("button")).find((b) => b.textContent?.trim() === 'build')
      expect((chip as HTMLElement).title).toContain('go build ./...')
      expect(host.querySelector('input[aria-label="命令行"]')).toBeNull()
    } finally {
      h2.quickCommands = null
    }
  })

  // 0.0.35 方案模式：开关点击翻转 store.planMode 并反映到 aria-pressed（可访问性）
  it('方案模式开关：点击翻转 store.planMode', async () => {
    mountComposer('', null)
    const { useChatStore } = await import('../stores/chat')
    const store = useChatStore()
    const btn = Array.from(host.querySelectorAll("button")).find((b) => b.textContent?.trim() === '方案')
    expect(btn).toBeTruthy()
    expect(store.planMode).toBe(false)
    ;(btn as HTMLElement).click()
    await nextTick()
    expect(store.planMode).toBe(true)
    expect((btn as HTMLElement).getAttribute('aria-pressed')).toBe('true')
  })

  // 0.0.45 任务 2：输入区两行布局
  it('两行布局：textarea 与发送按钮不在同一个 flex 行容器中', async () => {
    mountComposer('', null)
    await nextTick()
    const ta = host.querySelector('textarea') as HTMLTextAreaElement
    const send = host.querySelector('button[aria-label="发送"]') as HTMLButtonElement
    expect(ta).toBeTruthy()
    expect(send).toBeTruthy()
    expect(ta.parentElement).not.toBe(send.parentElement)
    const toolbar = send.closest('.border-t')
    expect(toolbar).toBeTruthy()
    expect(toolbar?.contains(ta)).toBe(false)
  })

  it('键盘交互：Enter 发送；Shift+Enter 不发送；isComposing 为真时不发送', async () => {
    mountComposer('测试消息', null)
    await nextTick()
    const ta = host.querySelector('textarea') as HTMLTextAreaElement

    // Shift+Enter 换行不发送
    ta.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', shiftKey: true, bubbles: true }))
    await nextTick()
    expect(h2.sends).toHaveLength(0)

    // 输入法组词中 Enter 确认选词不发送
    ta.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', isComposing: true, bubbles: true }))
    await nextTick()
    expect(h2.sends).toHaveLength(0)

    // 单独 Enter 发送
    ta.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', shiftKey: false, bubbles: true }))
    await new Promise((r) => setTimeout(r, 0))
    expect(h2.sends).toEqual(['测试消息'])
  })

  // 0.0.45 任务 3：模型选择器合并
  it('模型选择器合并：DOM 中不再有“跟随默认渠道”下拉，只保留单一模型按钮', async () => {
    mountComposer('', null)
    await nextTick()
    expect(host.querySelector('select[aria-label="选择本轮使用的模型"]')).toBeNull()
    expect(host.textContent).not.toContain('跟随默认渠道')
    const modelBtn = host.querySelector('button[aria-haspopup="menu"]')
    expect(modelBtn).toBeTruthy()
  })

  it('点模型行：setActiveModel 被调用，发送时 store.send 的 model 为 undefined', async () => {
    mountComposer('发默认', null)
    const channels = useChannelStore()
    await channels.load()
    await nextTick()

    const modelBtn = host.querySelector('button[aria-haspopup="menu"]') as HTMLButtonElement
    modelBtn.click()
    await nextTick()

    const menu = host.querySelector('[role="menu"]')
    expect(menu).toBeTruthy()
    const items = Array.from(menu?.querySelectorAll('[role="menuitem"]') ?? []) as HTMLButtonElement[]
    const claudeItem = items.find((btn) => btn.textContent?.includes('claude-3-5-sonnet'))
    expect(claudeItem).toBeTruthy()
    claudeItem?.click()
    await nextTick()

    expect(h2.activeModelCalls).toContainEqual({ channelId: 'ch2', model: 'claude-3-5-sonnet' })

    const send = host.querySelector('button[aria-label="发送"]') as HTMLButtonElement
    send.click()
    await new Promise((r) => setTimeout(r, 0))
    expect(h2.sends).toEqual(['发默认'])
    expect(h2.modelSends).toHaveLength(0)
  })

  it('选“仅下一条用它”：按钮显示“本轮：X” → 发送成功 → store.send 收到 model: X，之后恢复显示默认模型；再发送一条 model 为 undefined', async () => {
    mountComposer('发临时', null)
    const channels = useChannelStore()
    await channels.load()
    await nextTick()

    const modelBtn = host.querySelector('button[aria-haspopup="menu"]') as HTMLButtonElement
    modelBtn.click()
    await nextTick()

    const tempBtns = Array.from(host.querySelectorAll('button')).filter((b) => b.textContent?.trim() === '仅下一条用它')
    expect(tempBtns.length).toBeGreaterThan(0)
    ;(tempBtns[1] as HTMLButtonElement).click()
    await nextTick()

    expect(modelBtn.textContent).toContain('本轮：')
    const chosenModelName = (modelBtn.textContent ?? '').replace(/.*本轮：\s*/, '').trim()
    expect(chosenModelName).toBe('gpt-4o-mini')

    const send = host.querySelector('button[aria-label="发送"]') as HTMLButtonElement
    send.click()
    await new Promise((r) => setTimeout(r, 0))

    expect(h2.modelSends).toHaveLength(1)
    expect(h2.modelSends[0]).toEqual({ text: '发临时', model: 'gpt-4o-mini' })
    await nextTick()
    expect(modelBtn.textContent).not.toContain('本轮：')

    // 模拟第一轮回合结束（后端推送 terminal 终态），再发一条：model 为 undefined
    const store = useChatStore()
    store.running = false
    draftRef.value = '发第二条'
    await nextTick()
    send.click()
    await new Promise((r) => setTimeout(r, 0))
    expect(h2.sends).toEqual(['发第二条'])
  })

  it('临时模型下发送失败：仍显示“本轮：X”', async () => {
    mountComposer('测试失败', null)
    const channels = useChannelStore()
    await channels.load()
    await nextTick()

    const modelBtn = host.querySelector('button[aria-haspopup="menu"]') as HTMLButtonElement
    modelBtn.click()
    await nextTick()

    const tempBtn = Array.from(host.querySelectorAll('button')).find((b) => b.textContent?.trim() === '仅下一条用它')
    ;(tempBtn as HTMLButtonElement).click()
    await nextTick()
    expect(modelBtn.textContent).toContain('本轮：')

    h2.sendFails = true
    const send = host.querySelector('button[aria-label="发送"]') as HTMLButtonElement
    send.click()
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    expect(modelBtn.textContent).toContain('本轮：')
  })

  it('点 ✕：临时模型清除，setActiveModel 未被调用', async () => {
    mountComposer('', null)
    const channels = useChannelStore()
    await channels.load()
    await nextTick()

    const modelBtn = host.querySelector('button[aria-haspopup="menu"]') as HTMLButtonElement
    modelBtn.click()
    await nextTick()

    const tempBtn = Array.from(host.querySelectorAll('button')).find((b) => b.textContent?.trim() === '仅下一条用它')
    ;(tempBtn as HTMLButtonElement).click()
    await nextTick()
    expect(modelBtn.textContent).toContain('本轮：')

    const clearBtn = host.querySelector('button[aria-label="清除本轮模型"]') as HTMLButtonElement
    expect(clearBtn).toBeTruthy()
    clearBtn.click()
    await nextTick()

    expect(modelBtn.textContent).not.toContain('本轮：')
    expect(h2.activeModelCalls).toHaveLength(0)
  })

  it('模型菜单关闭：可用 Esc 关闭，点外部关闭', async () => {
    mountComposer('', null)
    await nextTick()

    const modelBtn = host.querySelector('button[aria-haspopup="menu"]') as HTMLButtonElement
    modelBtn.click()
    await nextTick()
    expect(host.querySelector('[role="menu"]')).toBeTruthy()

    // 点击外部关闭
    document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await nextTick()
    expect(host.querySelector('[role="menu"]')).toBeNull()

    // Esc 关闭
    modelBtn.click()
    await nextTick()
    expect(host.querySelector('[role="menu"]')).toBeTruthy()
    expect(consumeEsc()).toBe(true)
    await nextTick()
    expect(host.querySelector('[role="menu"]')).toBeNull()
  })

  it('首次引导：模型列表为空 → 发送按钮 disabled 且悬停提示"请先配置模型渠道"；非空时可用', async () => {
    // 模拟无模型渠道
    h2.channels = { channels: [], activeId: '' }
    mountComposer('有草稿文字', null)
    await useChannelStore().load()
    await nextTick()

    const sendBtn = host.querySelector('button[aria-label="发送"]') as HTMLButtonElement
    expect(sendBtn.disabled).toBe(true)
    expect(sendBtn.getAttribute('title')).toBe('请先配置模型渠道')

    // 恢复渠道
    h2.channels = {
      channels: [
        { id: 'ch1', name: '官方渠道', protocol: 'openai', baseUrl: '', model: 'gpt-4o', apiKey: 'k1', models: ['gpt-4o'] },
      ],
      activeId: 'ch1',
    }
    await useChannelStore().load()
    await nextTick()

    expect(sendBtn.disabled).toBe(false)
    expect(sendBtn.getAttribute('title')).toBe('发送（Enter）')
  })

  it('输入框历史回溯：会话有两条用户消息，空输入框按 ↑ 得到第二条、再 ↑ 得到第一条、↓ 回到第二条、再 ↓ 为空', async () => {
    mountComposer('', null)
    await nextTick()
    const store = useChatStore()
    store.messages.push(
      { id: '1', role: 'user', content: '第一条消息', at: 100 },
      { id: '2', role: 'assistant', content: '助手回复', at: 101 },
      { id: '3', role: 'user', content: '第二条消息', at: 102 },
    )
    await nextTick()

    const textarea = host.querySelector('textarea') as HTMLTextAreaElement
    expect(textarea.value).toBe('')

    // 1. 空输入框按 ↑ → 得到第二条（最新的用户消息）
    textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }))
    await nextTick()
    expect(textarea.value).toBe('第二条消息')

    // 2. 再按 ↑ → 得到第一条
    textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }))
    await nextTick()
    expect(textarea.value).toBe('第一条消息')

    // 3. 按 ↓ → 回到第二条
    textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    await nextTick()
    expect(textarea.value).toBe('第二条消息')

    // 4. 再按 ↓ → 恢复为空
    textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
    await nextTick()
    expect(textarea.value).toBe('')
  })

  it('输入框历史回溯：输入框非空时按 ↑ 不改变内容；/ 菜单打开时按 ↑ 只移动菜单选中项', async () => {
    mountComposer('已有用户输入', null)
    seedCatalog()
    await nextTick()
    const store = useChatStore()
    store.messages.push(
      { id: '1', role: 'user', content: '历史消息', at: 100 },
    )
    await nextTick()

    const textarea = host.querySelector('textarea') as HTMLTextAreaElement
    expect(textarea.value).toBe('已有用户输入')

    // 输入框非空时按 ↑ 不改变内容
    textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }))
    await nextTick()
    expect(textarea.value).toBe('已有用户输入')

    // / 菜单打开时按 ↑ 只移动菜单选中项，不触发历史回溯
    draftRef.value = '/'
    await nextTick()
    textarea.dispatchEvent(new Event('keyup', { bubbles: true }))
    await nextTick()

    const menu = host.querySelector('[aria-label="指定技能或 MCP"]')
    expect(menu).toBeTruthy()

    textarea.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }))
    await nextTick()
    // 输入框内容不被历史消息覆盖
    expect(textarea.value).toBe('/')
  })

  it('会话 token 用量显示：mock 返回 12345 → 显示"12.3k"；reportedAll=false → 显示"≥"；全 0 → 不显示', async () => {
    // 1. 全 0（离线桩或无用量）时：不显示
    h2.sessionUsage = {
      promptTokens: 0,
      completionTokens: 0,
      totalTokens: 0,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      turns: 0,
      reportedAll: true,
    }
    mountComposer('', null)
    await nextTick()
    await nextTick()
    expect(host.querySelector('[data-testid="session-tokens"]')).toBeNull()

    // 2. mock 返回 12345，reportedAll=true → 显示 "12.3k"
    h2.sessionUsage = {
      promptTokens: 8000,
      completionTokens: 4345,
      totalTokens: 12345,
      cacheReadTokens: 1000,
      cacheWriteTokens: 500,
      turns: 3,
      reportedAll: true,
    }
    const store = useChatStore()
    // 切换会话 ID 触发刷新
    store.sessionId = 'session-123'
    await nextTick()
    await nextTick()

    const el = host.querySelector('[data-testid="session-tokens"]') as HTMLElement
    expect(el).toBeTruthy()
    expect(el.textContent).toContain('12.3k')
    expect(el.textContent).not.toContain('≥')
    expect(el.title).toContain('12,345')
    expect(el.title).toContain('输入：8,000')
    expect(el.title).toContain('输出：4,345')

    // 3. reportedAll=false → 显示 "≥"
    h2.sessionUsage = {
      promptTokens: 8000,
      completionTokens: 4345,
      totalTokens: 12345,
      cacheReadTokens: 1000,
      cacheWriteTokens: 500,
      turns: 3,
      reportedAll: false,
    }
    // 切换会话 ID 再次刷新
    store.sessionId = 'session-456'
    await nextTick()
    await nextTick()

    const elIncomplete = host.querySelector('[data-testid="session-tokens"]') as HTMLElement
    expect(elIncomplete).toBeTruthy()
    expect(elIncomplete.textContent).toContain('12.3k')
    expect(elIncomplete.textContent).toContain('≥')
    expect(elIncomplete.title).toContain('（部分轮次上游未报告用量，数字为下限）')
  })

  it('会话 token 用量显示：触发终态事件（chat:terminal）后重新调用 SessionUsage', async () => {
    h2.sessionUsage = {
      promptTokens: 1000,
      completionTokens: 500,
      totalTokens: 1500,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      turns: 1,
      reportedAll: true,
    }
    mountComposer('', null)
    const store = useChatStore()
    store.sessionId = 'session-terminal-test'
    await nextTick()
    await nextTick()

    const initialCalls = h2.sessionUsageCalls.length
    expect(initialCalls).toBeGreaterThan(0)

    // 模拟终态事件触发
    h2.sessionUsage = {
      promptTokens: 2000,
      completionTokens: 1000,
      totalTokens: 3000,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      turns: 2,
      reportedAll: true,
    }
    const cbs = h2.eventCallbacks['chat:terminal'] ?? []
    expect(cbs.length).toBeGreaterThan(0)
    for (const cb of cbs) {
      cb({ sessionID: 'session-terminal-test', endReason: 0, error: '' })
    }
    await nextTick()
    await nextTick()

    expect(h2.sessionUsageCalls.length).toBeGreaterThan(initialCalls)
    const el = host.querySelector('[data-testid="session-tokens"]') as HTMLElement
    expect(el).toBeTruthy()
    expect(el.textContent).toContain('3k')
  })
})
