import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, nextTick, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import TimelinePanel from './TimelinePanel.vue'
import { useChatStore } from '../stores/chat'

// 右栏「时间线」tab（0.0.20）：历轮一览 + 回滚确认。
// 核心契约：回滚必须过确认框；空文件轮不显示回滚项文案误导；错误可见。

const st = vi.hoisted(() => ({
  rounds: [] as { round: number; userSeq: number; text: string; files: { path: string; revertable: boolean }[] }[],
  failTimeline: '',
  revertCalls: [] as number[],
  revertError: '',
  confirms: [] as string[],
  confirmNext: true,
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      RoundTimeline: async () => {
        if (st.failTimeline) throw new Error(st.failTimeline)
        return st.rounds
      },
      RevertToRound: async (_sid: string, userSeq: number) => {
        st.revertCalls.push(userSeq)
        if (st.revertError) throw new Error(st.revertError)
        return { round: 1, restored: ['a.go'], skipped: [] }
      },
      ListSessionSummaries: async () => [],
      Replay: async () => [],
      ReplayTail: async () => ({ messages: [], total: 0, from: 0 }),
      ReplayOlder: async () => ({ messages: [], total: 0, from: 0 }),
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
}))

vi.mock('../composables/useDialogs', () => ({
  useDialogs: () => ({
    confirm: async (opts: { title?: string; message?: string }) => {
      st.confirms.push(`${opts.title ?? ''}|${opts.message ?? ''}`)
      return st.confirmNext
    },
    prompt: async () => null,
  }),
}))

let host: HTMLElement
let app: App | null = null
let pinia: ReturnType<typeof createPinia>

async function mountPanel(): Promise<HTMLElement> {
  host = document.createElement('div')
  document.body.appendChild(host)
  app = createApp(defineComponent({ render: () => h(TimelinePanel) }))
  app.use(pinia)
  app.mount(host)
  await nextTick()
  await nextTick()
  return host
}

beforeEach(() => {
  document.body.innerHTML = ''
  pinia = createPinia()
  setActivePinia(pinia)
  st.rounds = []
  st.failTimeline = ''
  st.revertCalls = []
  st.revertError = ''
  st.confirms = []
  st.confirmNext = true
  const store = useChatStore()
  store.sessionId = 's-1'
})

describe('TimelinePanel（右栏「时间线」tab）', () => {
  it('渲染历轮与文件（已回滚的划线显示）', async () => {
    st.rounds = [
      { round: 1, userSeq: 2, text: '第一轮', files: [{ path: 'a.go', revertable: false }] },
      { round: 2, userSeq: 5, text: '第二轮', files: [{ path: 'b.go', revertable: true }] },
    ]
    const el = await mountPanel()
    expect(el.textContent).toContain('第一轮')
    expect(el.textContent).toContain('第二轮')
    const rows = Array.from(el.querySelectorAll('.line-through'))
    expect(rows.some((r) => r.textContent?.includes('a.go'))).toBe(true) // 已消费 → 划线
    // 每轮都有回滚按钮
    expect(el.textContent).toContain('回滚到此轮之前')
  })

  it('回滚必须过确认框，确认后才调 RevertToRound；取消不调', async () => {
    st.rounds = [{ round: 2, userSeq: 5, text: '第二轮', files: [{ path: 'b.go', revertable: true }] }]
    const el = await mountPanel()
    const btn = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.includes('回滚到此轮之前')) as HTMLButtonElement
    btn.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 0))
    expect(st.confirms.some((c) => c.includes('回滚到此轮之前'))).toBe(true)
    expect(st.revertCalls).toEqual([5]) // 确认默认 true → 已调用

    st.confirmNext = false
    btn.click()
    await nextTick()
    await new Promise((r) => setTimeout(r, 0))
    expect(st.revertCalls).toEqual([5]) // 取消 → 不再调用
  })

  it('载入失败错误可见；会话为空不请求', async () => {
    st.failTimeline = '账本坏了'
    const el = await mountPanel()
    expect(el.textContent).toContain('账本坏了')

    document.body.innerHTML = ''
    const store = useChatStore()
    store.sessionId = ''
    app?.unmount()
    st.failTimeline = ''
    const el2 = await mountPanel()
    expect(el2.textContent).toContain('还没有轮次')
  })
})
