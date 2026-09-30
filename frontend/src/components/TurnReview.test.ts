import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, defineComponent, h } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import TurnReview from './TurnReview.vue'
import { useChatStore } from '../stores/chat'

// 第 2 批交互契约：面板默认折叠（一行汇总）；展开后按文件聚合——
// 同文件多次改动只占一行（改 N 次 + 累计 ±），多目录同名文件靠路径两段区分。

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      RevealInExplorer: async () => {},
      RestoreToolWrite: async () => 'ok',
      ListSessionSummaries: async () => [],
      Replay: async () => [],
      Send: async () => {},
      Stop: async () => {},
      RenameSession: async () => {},
      ResolveApproval: async () => {},
      ResolveAsk: async () => {},
      ApprovalPolicy: async () => [],
      SetApprovalPolicy: async () => {},
      DeleteSession: async () => {},
    },
    runtime: { EventsOn: () => {} },
  }),
  winClose: async () => {},
  winMinimize: async () => {},
  winToggleMaximize: async () => {},
}))

let host: HTMLElement
let app: ReturnType<typeof createApp> | null = null
let pinia: ReturnType<typeof createPinia>

async function mountTurnReview(): Promise<HTMLElement> {
  host = document.createElement('div')
  document.body.appendChild(host)
  app = createApp(defineComponent({ render: () => h(TurnReview) }))
  app.use(pinia)
  app.mount(host)
  await nextTick()
  return host
}

function teardown() {
  app?.unmount()
  app = null
  host?.remove()
}

describe('TurnReview（第 2 批：默认折叠 + 按文件聚合）', () => {
  beforeEach(() => {
    teardown()
    localStorage.clear() // 折叠态记忆会跨测试污染，逐例清空
    pinia = createPinia() // store 注入与组件共用同一 pinia（此前被 useChatStore 早于 mount 调用绊倒）
    setActivePinia(pinia)
  })

  // 默认折叠：只出标题行汇总，不铺开文件明细
  it('默认折叠为一行汇总', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onTool({
      sessionID: store.sessionId,
      name: 'fs',
      status: 'success',
      summary: 'w',
      callID: 'c1',
      title: 'internal/core/agent/agent.go',
      op: 'edit',
      diff: '-old\n+new\n',
    })
    const el = await mountTurnReview()
    expect(el.textContent).toContain('本轮变更（1 个文件 · 1 处）')
    expect(el.textContent).not.toContain('agent.go') // 折叠态不得出现文件行
    expect(el.textContent).toContain('+1')
    expect(el.textContent).toContain('-1')
  })

  // 同文件多次 replace：聚合为一行（改 N 次 + 累计 ±），不再一排同名条目
  it('同文件多次改动聚合为一行', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    for (const [callID, diff] of [
      ['c1', '-a\n+b\n'],
      ['c2', '-c\n+d\n'],
      ['c3', '-e\n+f\n'],
    ] as const) {
      store.onTool({
        sessionID: store.sessionId,
        name: 'fs',
        status: 'success',
        summary: 'w',
        callID,
        title: 'internal/core/agent/agent.go',
        op: 'edit',
        diff,
      })
    }
    const el = await mountTurnReview()
    expect(el.textContent).toContain('1 个文件 · 3 处')
    await (el.querySelector('button') as HTMLButtonElement).click() // 展开面板
    await nextTick()
    const text = el.textContent ?? ''
    expect(text).toContain('agent.go')
    expect(text).toContain('改 3 次')
    expect(text.match(/agent\.go/g)?.length).toBe(1) // 只占一行
    expect(text).toContain('+3')
    expect(text).toContain('-3')
  })

  // 多目录同名文件：路径两段显示（目录 + 末段），聚合按完整路径分开
  it('多目录同名文件按路径区分聚合', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onTool({
      sessionID: store.sessionId,
      name: 'fs',
      status: 'success',
      summary: 'w',
      callID: 'c1',
      title: 'internal/core/agent/agent.go',
      op: 'edit',
      diff: '-a\n+b\n',
    })
    store.onTool({
      sessionID: store.sessionId,
      name: 'fs',
      status: 'success',
      summary: 'w',
      callID: 'c2',
      title: 'app/agent.go',
      op: 'edit',
      diff: '-a\n+b\n',
    })
    const el = await mountTurnReview()
    expect(el.textContent).toContain('2 个文件 · 2 处')
    await (el.querySelector('button') as HTMLButtonElement).click()
    await nextTick()
    const text = el.textContent ?? ''
    expect(text).toContain('internal/core/agent/')
    expect(text).toContain('app/')
    expect(text.match(/agent\.go/g)?.length).toBe(2) // 两个不同文件各占一行
  })

  // 第 3 批：点文件行 → 右侧文件详情面板（不再内联展开 diff）
  it('点文件行打开右侧详情面板', async () => {
    const store = useChatStore()
    await store.newSession()
    await store.send('hi')
    store.onTool({
      sessionID: store.sessionId,
      name: 'fs',
      status: 'success',
      summary: 'w',
      callID: 'c1',
      title: 'internal/core/agent/agent.go',
      op: 'edit',
      diff: '-a\n+b\n',
    })
    const el = await mountTurnReview()
    await (el.querySelector('button') as HTMLButtonElement).click() // 展开面板（标题行）
    await nextTick()
    // 按文本定位文件行（标题行/撤回按钮之后；不依赖按钮顺序）
    // 注意：用 Array.from 而非展开运算符——tsconfig 未开 DOM.Iterable，NodeList 不可直接迭代
    const row = Array.from(el.querySelectorAll('button')).find(
      (b) => (b.textContent ?? '').includes('agent.go') && !(b.textContent ?? '').includes('本轮变更'),
    )
    expect(row).toBeTruthy()
    await (row as HTMLButtonElement).click()
    expect(store.fileDetailPath).toBe('internal/core/agent/agent.go')
  })
})
