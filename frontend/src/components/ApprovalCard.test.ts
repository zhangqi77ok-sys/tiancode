import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import ApprovalCard from './ApprovalCard.vue'
import type { ChatMsg } from '../stores/chat'

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ApprovalPolicy: async () => [],
      SetApprovalPolicy: async () => {},
      ResolveApproval: async () => {},
      ResolveAsk: async () => {},
      GetWorkspace: async () => 'D:/proj-a',
      SetWorkspace: async () => {},
      ListSessionSummaries: async () => [],
      Replay: async () => [],
      Send: async () => {},
      Stop: async () => {},
      RenameSession: async () => {},
      DeleteSession: async () => {},
    },
    runtime: { EventsOn: () => {} },
  }),
}))

const { useChatStore } = await import('../stores/chat')

let host: HTMLElement
let app: App | null = null

function mountCard(m: ChatMsg): HTMLElement {
  host = document.createElement('div')
  document.body.appendChild(host)
  app = createApp(defineComponent({ render: () => h(ApprovalCard, { m }) }))
  app.use(createPinia())
  app.mount(host)
  return host
}

function teardown() {
  app?.unmount()
  app = null
  host?.remove()
}

// 0.0.06：审批卡主内容是人能读的句子（工具名/工作目录/命令/危险信号），
// 原始 JSON 折进「详情」。允许/拒绝按钮与会话名保持不变。
describe('ApprovalCard（0.0.06 人话主内容）', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    teardown()
  })

  it('shell 卡：主内容是一句话，命令可见，危险命令给出警示', () => {
    const el = mountCard({
      role: 'approval',
      content: '',
      toolName: 'shell',
      approvalId: 'ap-1',
      sessionTitle: '修 bug',
      args: JSON.stringify({ command: 'rm -rf build' }),
    })
    expect(el.textContent).toContain('执行命令：rm -rf build')
    expect(el.textContent).toContain('可能删除或覆盖文件')
    expect(el.textContent).toContain('会话：修 bug')
    expect(el.textContent).toContain('允许执行')
    // 原始 JSON 在详情里仍可查
    expect(el.textContent).toContain('详情（原始参数）')
  })

  it('fs write 卡：给出路径与"修改磁盘文件"提示', () => {
    const el = mountCard({
      role: 'approval',
      content: '',
      toolName: 'fs',
      approvalId: 'ap-2',
      args: JSON.stringify({ action: 'write', path: 'src/a.go', content: 'x' }),
    })
    expect(el.textContent).toContain('写入文件：src/a.go')
    expect(el.textContent).toContain('修改磁盘上的文件')
  })

  it('非危险命令不虚报警示', () => {
    const el = mountCard({
      role: 'approval',
      content: '',
      toolName: 'shell',
      approvalId: 'ap-3',
      args: JSON.stringify({ command: 'go build ./...' }),
    })
    expect(el.textContent).toContain('执行命令：go build ./...')
    expect(el.textContent).not.toContain('可能删除或覆盖文件')
  })
})
