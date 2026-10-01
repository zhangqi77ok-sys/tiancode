import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createApp, nextTick, type App } from 'vue'

// 空态（0.3 重排）：一件事——当前模型、当前工作区、三条可点的建议。
// 不再堆灰卡片、不再一排药丸；纯对话是**一等模式**（中性陈述，不出现警示色），
// 建议文案与实际能力一致（纯对话不提文件/命令）。

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      GetWorkspace: async () => '',
      ListChannels: async () => ({ channels: [], activeId: '' }),
    },
    runtime: { EventsOn: () => {} },
  }),
}))

const { useChatStore } = await import('../stores/chat')
const { useWorkspaceStore } = await import('../stores/workspace')
const MessageList = (await import('./MessageList.vue')).default

let host: HTMLElement
let app: App | null = null

function mountList(): HTMLElement {
  host = document.createElement('div')
  document.body.appendChild(host)
  const pinia = createPinia()
  setActivePinia(pinia)
  app = createApp(MessageList)
  app.use(pinia)
  app.mount(host)
  return host
}

const text = () => host.textContent ?? ''
const buttons = () => Array.from(host.querySelectorAll('button')) as HTMLButtonElement[]
const chips = () => buttons().filter((b) => b.className.includes('chip'))

beforeEach(() => {
  document.body.innerHTML = ''
})

afterEach(() => {
  app?.unmount()
  app = null
})

describe('MessageList 空态（0.3：一件事）', () => {
  it('无工作区：模型名 + 纯对话陈述 + 三条建议，无灰卡片、无药丸、无警示色', async () => {
    mountList() // 先挂载：setActivePinia 在里面完成，之后取到的就是组件用的那份 store
    useWorkspaceStore().path = ''
    // 全新 store 没有消息（空态的前提）
    expect(useChatStore().messages).toHaveLength(0)
    await nextTick()

    // 模型名是主读数（未配置时如实写"未选择模型"）
    expect(text()).toContain('未选择模型')
    // 纯对话是正面陈述，不是缺东西的警告
    expect(text()).toContain('纯对话 · 直接提问即可')
    expect(text()).not.toContain('未选择工作区')
    expect(text()).not.toContain('不可用')
    // 不得有警示色（warn 令牌只留给真正的失败态）
    expect(host.querySelector('[class*="warn"]'), '空态不得出现警示色').toBeFalsy()
    // 建议是三条安静的入口：不再是 chip 药丸，也不再有灰卡片容器
    const suggestions = buttons().filter((b) => !b.className.includes('chip'))
    expect(suggestions).toHaveLength(3)
    expect(chips()).toHaveLength(0)
    expect(host.querySelector('.rounded-\\[var\\(--r-card\\)\\]'), '不再堆灰卡片').toBeFalsy()
    // 纯对话组：不提"读文件/跑测试"这类本地工具才有的事
    expect(suggestions.map((c) => c.textContent ?? '').join('|')).toContain('快排')
    // 点击建议 → 回填输入框（suggest 事件由 App 接）
    suggestions[0].click()
  })

  it('有工作区：显示工作区名与能力，建议换成项目相关三条', async () => {
    mountList()
    useWorkspaceStore().path = 'D:\\work\\proj\\app'
    expect(useChatStore().messages).toHaveLength(0)
    await nextTick()

    expect(text()).toContain('工作区 proj/app')
    expect(text()).toContain('可读写文件')
    const suggestions = buttons().filter((b) => !b.className.includes('chip'))
    expect(suggestions).toHaveLength(3)
    expect(suggestions.map((c) => c.textContent ?? '').join('|')).toContain('介绍这个项目')
  })
})
