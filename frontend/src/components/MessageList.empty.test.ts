import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createApp, nextTick, type App } from 'vue'

// 阶段 1（空态）：不再是"一行灰字 + 三颗芯片"——当前模型、有没有工作区、三条建议都要看得见；
// 无工作区时主按钮是「选择工作区」，有工作区时不再出现（本地工具都已注册，不必再请人解锁）。
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

describe('MessageList 空态（阶段 1）', () => {
  it('无工作区：模型 / 纯对话状态 / 「选择工作区」主按钮 / 三条建议', async () => {
    mountList() // 先挂载：setActivePinia 在里面完成，之后取到的就是组件用的那份 store
    useWorkspaceStore().path = ''
    // 全新 store 没有消息（空态的前提）
    expect(useChatStore().messages).toHaveLength(0)
    await nextTick()

    expect(text()).toContain('模型')
    expect(text()).toContain('未选择工作区')
    expect(text()).toContain('文件与命令工具不可用')
    expect(buttons().some((b) => (b.textContent ?? '').includes('选择工作区'))).toBe(true)
    expect(chips()).toHaveLength(3)
    // 纯对话组：不提"读文件/跑测试"这类本地工具才有的事
    expect(chips().map((c) => c.textContent ?? '').join('|')).toContain('快排')
  })

  it('有工作区：显示工作区名，「选择工作区」不再出现，建议换成项目相关三条', async () => {
    mountList()
    useWorkspaceStore().path = 'D:\\work\\proj\\app'
    expect(useChatStore().messages).toHaveLength(0)
    await nextTick()

    expect(text()).toContain('工作区 proj/app')
    expect(buttons().some((b) => (b.textContent ?? '').includes('选择工作区'))).toBe(false)
    expect(chips()).toHaveLength(3)
    expect(chips().map((c) => c.textContent ?? '').join('|')).toContain('介绍这个项目')
  })
})
