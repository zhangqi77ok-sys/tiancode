import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, defineComponent, h } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import ToolCard from './ToolCard.vue'
import type { ChatMsg } from '../stores/chat'

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      RevealInExplorer: async () => {},
      RestoreToolWrite: async (_sid: string, callID: string) => `已恢复 ${callID}`,
      GetWorkspace: async () => 'D:/w',
      SetWorkspace: async () => {},
      ApprovalPolicy: async () => [],
      SetApprovalPolicy: async () => {},
      ResolveApproval: async () => {},
      ResolveAsk: async () => {},
      ListSessionSummaries: async () => [],
      Replay: async () => [],
      Send: async () => {},
      Stop: async () => {},
      RenameSession: async () => {},
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

function mountCard(m: ChatMsg): HTMLElement {
  host = document.createElement('div')
  document.body.appendChild(host)
  const pinia = createPinia()
  app = createApp(defineComponent({ render: () => h(ToolCard, { m }) }))
  app.use(pinia)
  setActivePinia(pinia) // 组件与测试共用同一 pinia
  app.mount(host)
  return host
}

function teardown() {
  app?.unmount()
  app = null
  host?.remove()
}

describe('ToolCard（0.0.06 交互契约）', () => {
  beforeEach(() => {
    teardown()
  })

  // write/edit（有 diff）默认展开：变更就是卡的主体，不能靠矮容器藏住
  it('write 卡默认展开 diff', () => {
    const el = mountCard({
      role: 'tool',
      content: 'written a.go',
      toolName: 'fs',
      status: 'success',
      op: 'write',
      title: 'a.go',
      diff: '--- a.go\n+++ a.go\n@@\n-old\n+new\n',
    })
    expect(el.querySelector('pre')?.textContent ?? '').not.toContain('-old') // diff 面板不是 pre
    expect(el.textContent).toContain('+new')
    expect(el.textContent).toContain('-old')
  })

  // 只读卡默认折叠（详情可手动展开）
  it('read 卡默认折叠', () => {
    const el = mountCard({
      role: 'tool',
      content: 'file body',
      toolName: 'fs',
      status: 'success',
      op: 'read',
      title: 'b.txt',
    })
    expect(el.querySelector('pre')).toBeNull()
  })

  // 执行中：强制展开且点击不折叠（结束后才允许收）
  it('running 卡强制展开且点击不折叠', async () => {
    const el = mountCard({
      role: 'tool',
      content: '执行中…',
      toolName: 'shell',
      status: 'running',
      op: 'exec',
      title: 'npm test',
    })
    expect(el.textContent).toContain('执行中')
    await (el.querySelector('button') as HTMLButtonElement).click()
    await nextTick()
    expect(el.querySelector('pre')).not.toBeNull() // 仍然展开
  })

  // "在资源管理器中显示"：只对文件类动作出现
  it('write/edit/read/list/tree 卡提供在资源管理器中显示', () => {
    const el = mountCard({
      role: 'tool',
      content: 'written a.go',
      toolName: 'fs',
      status: 'success',
      op: 'write',
      title: 'a.go',
      diff: '+x\n',
    })
    expect(el.textContent).toContain('在资源管理器中显示')
  })

  // 0.0.07：带撤销快照的 write 卡提供"恢复写入前"；不可恢复说明可见
  it('write 卡提供恢复写入前，undoNote 透出', async () => {
    const el = mountCard({
      role: 'tool',
      content: 'written a.go',
      toolName: 'fs',
      status: 'success',
      op: 'write',
      title: 'a.go',
      callId: 'call-9',
      hasUndo: true,
      undoPath: 'a.go',
      diff: '+new\n',
    })
    const btn = el.querySelector<HTMLButtonElement>('button.chip')
    expect(el.textContent).toContain('恢复写入前')
    await btn?.click()
    await nextTick()
    // 点击后调用后端（mock 返回成功文案），卡片进入恢复中态
    expect(el.textContent).toContain('正在恢复')
  })

  it('undoNote 优先可见（无法恢复的场景）', () => {
    const el = mountCard({
      role: 'tool',
      content: 'written big.bin',
      toolName: 'fs',
      status: 'success',
      op: 'write',
      title: 'big.bin',
      undoNote: '这次无法恢复：写入前的内容超过上限，未保存恢复数据',
    })
    expect(el.textContent).toContain('这次无法恢复')
    expect(el.textContent).not.toContain('恢复写入前')
  })
})
