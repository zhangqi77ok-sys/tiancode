import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import GlobalSearch from './GlobalSearch.vue'
import { useChatStore } from '../stores/chat'

// 跨会话搜索面板（0.0.23）：点结果 = 切会话 + 跳到那一轮（复用时间线跳转链路）。
// 模态是 Teleport 出去的：一律在 document 上查（ToneSettings.test 同款纪律）。

const h2 = vi.hoisted(() => ({
  calls: [] as Array<{ query: string; workspace: string; limit: number }>,
  hits: [] as unknown[],
  stats: { scannedLedgers: 0, truncatedLedgers: 0, byteLimitPerLedger: 0 },
  selectSession: [] as string[],
  jumpToSeq: [] as number[],
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      SearchSessions: async (query: string, workspace: string, limit: number) => {
        h2.calls.push({ query, workspace, limit })
        return { hits: h2.hits, query, stats: h2.stats }
      },
      ListChannels: async () => ({ channels: [], activeId: '' }),
      GetWorkspace: async () => '',
      // selectSession 真实实现会走尾屏投影（组件里不 mock store 的其他方法）
      ReplayTail: async () => ({ messages: [], total: 0, from: 0 }),
      ReplayOlder: async () => ({ messages: [], total: 0, from: 0 }),
      ListSessionSummaries: async () => [],
    },
    runtime: { EventsOn: () => {} },
  }),
  winClose: async () => {},
  winMinimize: async () => {},
  winToggleMaximize: async () => {},
}))

let app: App | null = null

async function mountPanel() {
  document.body.innerHTML = ''
  const host = document.createElement('div')
  document.body.appendChild(host)
  const pinia = createPinia()
  setActivePinia(pinia)
  app = createApp(GlobalSearch)
  app.use(pinia)
  app.mount(host)
  await nextTick()
  const store = useChatStore()
  const origSelect = store.selectSession
  store.selectSession = (async (id: string) => {
    h2.selectSession.push(id)
    return origSelect(id)
  }) as typeof store.selectSession
  store.jumpToSeq = (async (seq: number) => {
    h2.jumpToSeq.push(seq)
  }) as typeof store.jumpToSeq
}

const text = () => document.body.textContent ?? ''
const input = () => document.querySelector('input[type="search"]') as HTMLInputElement
const hitBtn = (t: string) =>
  Array.from(document.querySelectorAll('button')).find((b) => (b.textContent ?? '').includes(t)) as
    | HTMLButtonElement
    | undefined

async function search(q: string) {
  const el = input()
  el.value = q
  el.dispatchEvent(new Event('input'))
  await nextTick()
  el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }))
  await nextTick()
  await nextTick()
  await nextTick()
}

beforeEach(() => {
  app?.unmount()
  app = null
  document.body.innerHTML = ''
  h2.calls = []
  h2.hits = []
  h2.stats = { scannedLedgers: 0, truncatedLedgers: 0, byteLimitPerLedger: 0 }
  h2.selectSession = []
  h2.jumpToSeq = []
})

describe('GlobalSearch（跨会话搜索）', () => {
  it('回车搜索：把关键词与范围发给后端并列出命中', async () => {
    h2.hits = [
      {
        sessionID: 's-a',
        sessionTitle: '排查内存泄漏',
        workspace: 'D:/proj-a',
        lastActiveMs: 1,
        role: 'assistant',
        anchorSeq: 7,
        snippet: '泄漏通常来自 channel 未关闭',
      },
    ]
    await mountPanel()
    await search('泄漏')

    expect(h2.calls).toEqual([{ query: '泄漏', workspace: '', limit: 50 }])
    expect(text()).toContain('排查内存泄漏')
    expect(text()).toContain('泄漏通常来自 channel 未关闭')
  })

  it('点命中：先切会话再跳到该轮（anchorSeq 透传）', async () => {
    h2.hits = [
      {
        sessionID: 's-b',
        sessionTitle: '上周那场',
        workspace: '',
        lastActiveMs: 1,
        role: 'user',
        anchorSeq: 12,
        snippet: '我上周问过这个',
      },
    ]
    await mountPanel()
    await search('上周')
    const hit = hitBtn('上周那场')
    expect(hit).toBeTruthy()
    await (hit as HTMLButtonElement).click()
    // openHit 内是 selectSession（含投影 await）→ jumpToSeq 的串行链：等宏任务跑完
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()
    expect(h2.selectSession).toEqual(['s-b'])
    expect(h2.jumpToSeq).toEqual([12])
  })

  it('无命中且没有长账本被截断：断言"不存在"（结果完整，说"没有"是诚实的）', async () => {
    h2.hits = []
    await mountPanel()
    await search('不存在的词')
    expect(text()).toContain('没有会话里包含这个搜索词')
  })

  // 0.0.30 审查 R1：长账本只扫了开头一段，零命中时的含义是"没扫到"而非"不存在"。
  // 旧实现把后者说成前者，害用户在有命中时以为那句话没出现过。
  it('无命中但有长账本被截断：如实写明只扫了前 N MB，不得断言"没有"', async () => {
    h2.hits = []
    h2.stats = { scannedLedgers: 3, truncatedLedgers: 2, byteLimitPerLedger: 4 * 1024 * 1024 }
    await mountPanel()
    await search('只在最近的话里出现的词')
    const t = text()
    expect(t).toContain('没有搜到')
    expect(t).toContain('2 本长会话只扫了开头 4MB')
    expect(t).not.toContain('没有会话里包含这个搜索词')
  })

  it('有命中且有长账本被截断：结果上方写明结果可能不全', async () => {
    h2.hits = [
      {
        sessionID: 's-c',
        sessionTitle: '长会话',
        workspace: '',
        lastActiveMs: 1,
        role: 'user',
        anchorSeq: 3,
        snippet: '命中片段',
        truncated: true,
      },
    ]
    h2.stats = { scannedLedgers: 3, truncatedLedgers: 1, byteLimitPerLedger: 4 * 1024 * 1024 }
    await mountPanel()
    await search('命中片段')
    const t = text()
    expect(t).toContain('有 1 本长会话只扫了开头 4MB')
    expect(t).toContain('这部分结果可能不全')
    // 单条命中上的精确标注（本账本没扫完）保留
    expect(t).toContain('扫描已截断')
  })
})
