import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createApp, nextTick, type App } from 'vue'

// 空态（0.0.27）：纯对话是**一等模式**，不是"缺工作区"的警告——两种状态同一视觉权重、
// 同一中性色（不得出现警示色）；挂工作区是可选增强（次要按钮，无工作区时才出现）。
// 建议文案与能力必须一致：纯对话不提文件/命令，有工作区才给项目相关建议。

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

describe('MessageList 空态（0.0.27：纯对话一等化）', () => {
  it('无工作区：中性陈述「纯对话」+ 次要的「挂上工作区（可选）」，不得有警示色与主按钮', async () => {
    mountList() // 先挂载：setActivePinia 在里面完成，之后取到的就是组件用的那份 store
    useWorkspaceStore().path = ''
    // 全新 store 没有消息（空态的前提）
    expect(useChatStore().messages).toHaveLength(0)
    await nextTick()

    expect(text()).toContain('模型')
    // 纯对话是正面陈述，不是缺东西的警告
    expect(text()).toContain('纯对话 · 直接提问即可')
    expect(text()).not.toContain('未选择工作区')
    expect(text()).not.toContain('不可用')
    // 不得有警示色（warn 令牌只留给真正的失败态）
    expect(host.querySelector('[class*="warn"]'), '空态不得出现警示色').toBeFalsy()
    // 挂工作区是可选的次要按钮：存在，但不是主按钮样式
    const wsBtn = buttons().find((b) => (b.textContent ?? '').includes('挂上工作区'))
    expect(wsBtn, '无工作区时要提供"挂上工作区"入口').toBeTruthy()
    expect(wsBtn?.className).not.toContain('btn-primary')
    expect(chips()).toHaveLength(3)
    // 纯对话组：不提"读文件/跑测试"这类本地工具才有的事
    expect(chips().map((c) => c.textContent ?? '').join('|')).toContain('快排')
  })

  it('有工作区：显示工作区名与能力，不再出现挂工作区按钮，建议换成项目相关三条', async () => {
    mountList()
    useWorkspaceStore().path = 'D:\\work\\proj\\app'
    expect(useChatStore().messages).toHaveLength(0)
    await nextTick()

    expect(text()).toContain('工作区 proj/app')
    expect(text()).toContain('可读写文件')
    expect(buttons().some((b) => (b.textContent ?? '').includes('挂上工作区'))).toBe(false)
    expect(chips()).toHaveLength(3)
    expect(chips().map((c) => c.textContent ?? '').join('|')).toContain('介绍这个项目')
  })
})
