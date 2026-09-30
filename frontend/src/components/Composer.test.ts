import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, ref } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import Composer from './Composer.vue'
import DialogHost from './DialogHost.vue'
import { useChatStore, type PendingAttachment } from '../stores/chat'
import { useCatalogStore } from '../stores/catalog'

// 第 7 批：重跑 = 回填 → 用户改字 → 按发送时确认分叉（RerunFrom）→ 发送**输入框里的文字**；
// 取消确认时什么都不撤回。这里用真实 DialogHost 驱动确认框（点它的按钮），不 mock 对话框。

const h2 = vi.hoisted(() => ({
  sends: [] as string[],
  attach: [] as { text: string; attachments: string; forceTool: string }[],
  rerunCalls: [] as { seq: number }[],
  probes: [] as string[],
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      Send: async (_sid: string, text: string) => {
        h2.sends.push(text)
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
      Stop: async () => {},
    },
    runtime: { EventsOn: () => {} },
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
    h2.attach = []
    h2.rerunCalls = []
    h2.probes = []
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
})
