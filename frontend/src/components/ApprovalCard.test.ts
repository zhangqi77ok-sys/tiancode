import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h, type App } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import ApprovalCard from './ApprovalCard.vue'
import type { ChatMsg } from '../stores/chat'

const allowPrefixCalls: Array<[string, string]> = []

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ApprovalPolicy: async () => [],
      SetApprovalPolicy: async () => {},
      ResolveApproval: async () => {},
      ResolveApprovalAllowPrefix: async (id: string, prefix: string) => {
        allowPrefixCalls.push([id, prefix])
      },
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

// 2026-10-19：跑测试/构建要反复点审批——shell 卡给「本会话总是允许「前缀」」，
// 按钮上写的前缀就是提交给后端的规则；没有建议前缀（组合命令等）或非 shell 不出按钮。
describe('ApprovalCard 本会话总是允许此前缀', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    teardown()
    allowPrefixCalls.length = 0
  })

  it('shell 卡带建议前缀：显示按钮，点击按原前缀提交并转为已允许', async () => {
    const m: ChatMsg = {
      role: 'approval',
      content: '',
      toolName: 'shell',
      approvalId: 'ap-9',
      allowPrefix: 'go test',
      args: JSON.stringify({ command: 'go test ./...' }),
    }
    const el = mountCard(m)
    const btn = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.includes('本会话总是允许'))
    expect(btn?.textContent).toContain('本会话总是允许「go test」')
    btn!.click()
    await new Promise((r) => setTimeout(r, 0))
    expect(allowPrefixCalls).toEqual([['ap-9', 'go test']])
  })

  it('没有建议前缀或非 shell：不出按钮', () => {
    const el = mountCard({
      role: 'approval',
      content: '',
      toolName: 'shell',
      approvalId: 'ap-10',
      args: JSON.stringify({ command: 'echo a > out.txt' }),
    })
    expect(el.textContent).not.toContain('本会话总是允许')
    teardown()
    const el2 = mountCard({
      role: 'approval',
      content: '',
      toolName: 'browser',
      approvalId: 'ap-11',
      allowPrefix: 'x',
      args: '{}',
    })
    expect(el2.textContent).not.toContain('本会话总是允许')
  })
})
