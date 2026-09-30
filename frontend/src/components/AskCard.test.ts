import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, defineComponent, h } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import AskCard from './AskCard.vue'
import { useChatStore, type ChatMsg } from '../stores/chat'

// 0.0.11：无选项的 ask 卡必须有自由输入出口——此前只有一段问题文本、没有任何
// 输入，模型那一轮会被永久挂住（ask 阻塞等答复，用户却无从回答）。

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ResolveAsk: async () => {},
      ResolveApproval: async () => {},
      ListSessionSummaries: async () => [],
      Replay: async () => [],
      Send: async () => {},
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
let pinia: ReturnType<typeof createPinia>

async function mountCard(m: Partial<ChatMsg>): Promise<HTMLElement> {
  host = document.createElement('div')
  document.body.appendChild(host)
  app = createApp(
    defineComponent({ render: () => h(AskCard, { m: { role: 'ask', content: '', ...m } as ChatMsg }) }),
  )
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

describe('AskCard（0.0.11：无选项必须给输入出口）', () => {
  beforeEach(() => {
    teardown()
    pinia = createPinia()
    setActivePinia(pinia)
  })

  it('无选项时渲染自由输入并提交答复', async () => {
    const store = useChatStore()
    const spy = vi.spyOn(store, 'resolveAsk').mockResolvedValue(undefined)
    const el = await mountCard({ question: '要保留这段注释吗？', askId: 'a1', options: [] })
    const input = el.querySelector('input')
    expect(input).toBeTruthy()
    input!.value = '保留'
    input!.dispatchEvent(new Event('input'))
    await nextTick()
    const btn = el.querySelector('button') as HTMLButtonElement
    expect(btn).toBeTruthy()
    btn.click()
    await nextTick()
    expect(spy).toHaveBeenCalledWith('a1', '保留')
  })

  it('有选项时点选即答、不给输入框', async () => {
    const store = useChatStore()
    const spy = vi.spyOn(store, 'resolveAsk').mockResolvedValue(undefined)
    const el = await mountCard({ question: '选一个', askId: 'a2', options: ['A', 'B'] })
    expect(el.querySelector('input')).toBeFalsy()
    const btns = el.querySelectorAll('button')
    ;(btns[0] as HTMLButtonElement).click()
    await nextTick()
    expect(spy).toHaveBeenCalledWith('a2', 'A')
  })
})
