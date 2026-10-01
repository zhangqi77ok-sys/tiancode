import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createApp, nextTick, type App } from 'vue'

// 阶段 1（顶栏瘦身）的行为断言——不测 Tailwind 类名堆叠，测用户能看到/点到的东西：
//   · 定高不换行（1280 宽不折两行靠的是定高 + nowrap + 右侧不收缩）；
//   · 中段只显示工作区末段、完整路径进 title，没有就不占位；
//   · 图标菜单收齐「导出 / 命令确认 / 主题 / 工作区」，导出行为与改前一致；
//   · Esc 由菜单消费（浮层优先）——App 的全局 Esc 因此不会顺手中断正在跑的回合；
//   · 油表：未配置上限给短文案，配了上限才算剩余比例，折叠绝不静默。
const h = vi.hoisted(() => ({
  saved: [] as string[],
  markdown: '# 会话',
  branch: 'main',
  // 审批策略（0.2.36+）：SetApprovalPolicy 可控失败（测开关失败回滚）
  policyCalls: [] as string[][],
  setPolicyFails: false,
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      CurrentBranch: async () => h.branch,
      ListChannels: async () => ({ channels: [], activeId: '' }),
      ApprovalPolicy: async () => [],
      GetWorkspace: async () => '',
      SetApprovalPolicy: async (tools: string[]) => {
        if (h.setPolicyFails) throw new Error('策略保存失败')
        h.policyCalls.push(tools)
      },
      ExportSessionMarkdown: async () => h.markdown,
      SaveTextFile: async (name: string, content: string) => {
        h.saved.push(`${name}::${content}`)
        return 'D:\\x.md'
      },
    },
    runtime: { EventsOn: () => {} },
  }),
  winClose: async () => {},
  winMinimize: async () => {},
  winToggleMaximize: async () => {},
}))

const { useChatStore } = await import('../stores/chat')
const { consumeEsc } = await import('../composables/useEsc')
const AppHeader = (await import('./AppHeader.vue')).default

let host: HTMLElement
let app: App | null = null

function mountHeader(): HTMLElement {
  host = document.createElement('div')
  document.body.appendChild(host)
  const pinia = createPinia()
  setActivePinia(pinia)
  app = createApp(AppHeader)
  app.use(pinia)
  app.mount(host)
  return host
}

const headerEl = () => host.querySelector('header') as HTMLElement
const menuEl = () => host.querySelector('[role="menu"]') as HTMLElement | null
const menuItems = () => Array.from(host.querySelectorAll('[role="menu"] [role="menuitem"]')) as HTMLButtonElement[]
const text = () => headerEl().textContent ?? ''

async function openMenu() {
  ;(host.querySelector('button[aria-label="更多操作"]') as HTMLButtonElement).click()
  await nextTick()
}

beforeEach(() => {
  document.body.innerHTML = ''
  h.saved = []
  h.branch = 'main'
  h.policyCalls = []
  h.setPolicyFails = false
})

afterEach(() => {
  app?.unmount()
  app = null
})

describe('AppHeader（阶段 1）', () => {
  it('定高且不换行', async () => {
    mountHeader()
    await nextTick()
    const cls = headerEl().className
    expect(cls).toContain('h-10') // 约 40px
    expect(cls).toContain('flex-nowrap')
    expect(cls).toContain('shrink-0')
    expect(cls).not.toContain('flex-wrap')
    // 不折行不许用裁切换：图标菜单是 header 内的 absolute 浮层，overflow-hidden
    // 会把菜单整张剪掉——点"更多"就像没反应（0.0.17 实机反馈，jsdom 不模拟裁切，只能锁类名）
    expect(cls).not.toContain('overflow-hidden')
  })

  it('中段只显示工作区末段，完整路径进 title；两者都没有就不占位', async () => {
    h.branch = '' // 无分支（挂载时取一次，所以要在挂载前设）
    mountHeader()
    await nextTick()
    expect(text()).not.toContain('proj') // 草稿 + 无工作区：中段整段不占位
    expect(text()).not.toContain('main')

    h.branch = 'main'
    const store = useChatStore()
    store.sessionId = 's1' // 切会话触发重取分支
    store.summaries = [{ id: 's1', title: '', workspace: 'D:\\work\\proj\\app' }]
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()
    const cell = Array.from(headerEl().querySelectorAll('span')).find((s) => s.textContent?.trim() === 'app')
    expect(cell, '中段显示路径末段').toBeTruthy()
    expect(cell?.getAttribute('title')).toBe('D:\\work\\proj\\app')
    expect(text()).toContain('main') // 分支与工作区并列
  })

  it('图标菜单收齐四个入口，导出行为不变（另存为仍走 SaveTextFile）', async () => {
    mountHeader()
    await nextTick()
    useChatStore().sessionId = 's1'
    await openMenu()
    const labels = menuItems().map((b) => b.textContent ?? '')
    for (const want of ['复制 Markdown', '另存为文件', '命令确认', '主题']) {
      expect(labels.some((t) => t.includes(want)), `菜单缺 ${want}`).toBe(true)
    }
    // 工作区一组在菜单里（标题行是文字，不是条目）
    expect(menuEl()?.textContent ?? '').toContain('工作区')
    expect(labels.some((t) => t.includes('选择其他目录'))).toBe(true)
    const save = menuItems().find((b) => (b.textContent ?? '').includes('另存为文件'))
    expect(save).toBeTruthy()
    save?.click()
    await new Promise((r) => setTimeout(r, 0))
    expect(h.saved).toHaveLength(1)
    expect(h.saved[0]).toContain('# 会话')
  })

  it('Esc 由菜单消费（浮层优先）并关闭菜单', async () => {
    mountHeader()
    await openMenu()
    expect(menuEl()).toBeTruthy()
    // 消费栈里有消费者 = App 的全局 Esc 不会走到"中断生成"
    expect(consumeEsc()).toBe(true)
    await nextTick()
    expect(menuEl()).toBeNull()
  })

  it('油表：无读数不编比例；渠道上限与默认预算要能分辨', async () => {
    mountHeader()
    await nextTick()
    const store = useChatStore()
    store.promptTokens = 12345
    await nextTick()
    expect(text()).toContain('无预算读数') // 后端未上报读数时不编造百分比

    // 渠道声明了上限：给剩余比例 + 折叠标注，全文进 title
    store.contextInfo = {
      estimatedTokens: 12345,
      budgetTokens: 100000,
      budgetDefault: false,
      folded: 2,
      dropped: false,
    }
    await nextTick()
    expect(text()).toContain('余 88%')
    expect(text()).toContain('已折叠 2 项')
    const bar = headerEl().querySelector('[title*="上限 100k"]') as HTMLElement | null
    expect(bar, '完整读数（tok / 上限 / 剩余 / 折叠）在 title 里').toBeTruthy()

    // 阶段 5-2：渠道未声明上限 → 后端按保守默认预算折叠，油表必须写明来源，
    // 并给出"在哪覆盖"（否则用户会以为渠道里填过这个数）
    store.contextInfo = {
      estimatedTokens: 12345,
      budgetTokens: 32768,
      budgetDefault: true,
      folded: 0,
      dropped: false,
    }
    await nextTick()
    expect(text()).toContain('按默认预算')
    const noted = headerEl().querySelector('[title*="默认预算 33k"]') as HTMLElement | null
    expect(noted, '默认预算的完整读数在 title 里').toBeTruthy()
    expect(noted?.getAttribute('title')).toContain('渠道未声明上下文上限')
    expect(noted?.getAttribute('title')).toContain('contextLimit')

    // 折完仍超的两种含义必须分开（阶段 5-2 修订）：默认预算只是折叠阈值（照发），
    // 渠道上限是硬限制（不发请求）——写成同一句会让用户以为回合被拒了
    store.contextInfo = {
      estimatedTokens: 40000,
      budgetTokens: 32768,
      budgetDefault: true,
      folded: 3,
      dropped: true,
    }
    await nextTick()
    expect(text()).toContain('已尽量折叠')
    expect(text()).not.toContain('已达上限')
    expect(
      headerEl().querySelector('[title*="不是硬限制"]'),
      '默认预算下超了要说清"照发"',
    ).toBeTruthy()

    store.contextInfo = {
      estimatedTokens: 120000,
      budgetTokens: 100000,
      budgetDefault: false,
      folded: 3,
      dropped: true,
    }
    await nextTick()
    expect(text()).toContain('已达上限')
  })

  // 审批闸门开关：乐观翻转 + 失败回滚——SetApprovalPolicy 失败时若开关停在
  // 翻转态，显示就与实际策略相反（用户以为开着的确认闸实际是关的）
  it('命令确认开关：成功时保持新状态并下发策略', async () => {
    mountHeader()
    await new Promise((r) => setTimeout(r, 0)) // 等挂载初始化链跑完（初值读取在前）
    await nextTick()
    await openMenu()
    const item = menuItems().find((b) => (b.textContent ?? '').includes('命令确认')) as HTMLButtonElement
    expect(item.getAttribute('aria-pressed')).toBe('false')
    item.click()
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()
    expect(item.getAttribute('aria-pressed')).toBe('true')
    expect(item.textContent).toContain('开')
    expect(h.policyCalls).toEqual([['shell', 'ext_manage']])
  })

  it('命令确认开关：下发失败回滚到原状态（不假装开成功）', async () => {
    h.setPolicyFails = true
    mountHeader()
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()
    await openMenu()
    const item = menuItems().find((b) => (b.textContent ?? '').includes('命令确认')) as HTMLButtonElement
    item.click()
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()
    expect(item.getAttribute('aria-pressed')).toBe('false') // 回滚
    expect(item.textContent).toContain('关')
    expect(h.policyCalls).toEqual([])
    expect(useChatStore().error).toContain('策略保存失败') // 失败可见
  })
})
