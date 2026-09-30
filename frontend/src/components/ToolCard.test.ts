import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, defineComponent, h } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import ToolCard from './ToolCard.vue'
import type { ChatMsg } from '../stores/chat'

// 0.0.12：打开文件走 OpenInDefaultApp（记录调用做断言）
// 注意：变量名不能叫 h——本文件从 vue 引入了 render 用的 h，遮蔽会直接炸渲染
const openCalls = vi.hoisted(() => ({ opened: [] as { sid: string; path: string; line: number }[] }))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      RevealInExplorer: async () => {},
      // 第 8 批：可点路径走统一入口 OpenAtLine（后端决定用配置命令还是系统默认程序）
      OpenAtLine: async (sid: string, path: string, line: number) => {
        openCalls.opened.push({ sid, path, line })
      },
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
    openCalls.opened = []
  })

  // 0.0.12：搜索结果的命中行路径可点开文件（path:line: 不再是死文本）；
  // 上下文行（path-line-）不给按钮——"-数字-" 在带连字符的路径上会误判，宁可不点
  it('搜索卡：命中行路径可点开文件，且只打开文件不跳行', async () => {
    const el = mountCard({
      role: 'tool',
      content: 'src/a.go-2-func f() {\nsrc/a.go:3:NEEDLE\nsrc/a.go-4-}\n',
      toolName: 'search',
      status: 'success',
      op: 'search',
      title: 'NEEDLE',
    })
    // 默认折叠：先展开（详情按钮）
    ;(el.querySelector('button') as HTMLButtonElement).click()
    await nextTick()
    const pathBtns = el.querySelectorAll('button[title^="打开 "]')
    expect(pathBtns).toHaveLength(1) // 只有命中行可点
    ;(pathBtns[0] as HTMLButtonElement).click()
    await nextTick()
    expect(openCalls.opened).toEqual([{ sid: '', path: 'src/a.go', line: 3 }]) // 命中行号一并带上
  })

  // 0.0.12：files_only 的路径列表整行可点（没有 path:line: 形态时按路径列表渲染）
  it('文件列表卡：路径可点开，页脚不算路径', async () => {
    const el = mountCard({
      role: 'tool',
      content: 'internal/app/handler.go\ncore/agent/agent.go\n(truncated, max_matches 3)\n',
      toolName: 'search',
      status: 'success',
      op: 'search',
      title: 'handler\\.go$',
    })
    ;(el.querySelector('button') as HTMLButtonElement).click()
    await nextTick()
    const pathBtns = el.querySelectorAll('button[title^="打开 "]')
    expect(pathBtns).toHaveLength(2)
    ;(pathBtns[1] as HTMLButtonElement).click()
    await nextTick()
    expect(openCalls.opened).toEqual([{ sid: '', path: 'core/agent/agent.go', line: 0 }])
  })

  // 第 7 批：工具卡可复制输出全文（命令输出、搜索结果都是纯文本）
  it('命令卡提供复制输出', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const el = mountCard({
      role: 'tool',
      content: 'PASS\nok tiancode/internal/app',
      toolName: 'shell',
      status: 'success',
      op: 'exec',
      title: 'go test',
    })
    ;(el.querySelector('button') as HTMLButtonElement).click() // 展开
    await nextTick()
    const btn = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.includes('复制输出'))
    expect(btn).toBeTruthy()
    ;(btn as HTMLButtonElement).click()
    await nextTick()
    expect(writeText).toHaveBeenCalledWith('PASS\nok tiancode/internal/app')
  })

  // 第 7 批：有 diff 的卡复制的是 diff（"当前展示的全文"）
  it('写卡复制输出取 diff', async () => {
    const writeText = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const el = mountCard({
      role: 'tool',
      content: 'written a.go',
      toolName: 'fs',
      status: 'success',
      op: 'write',
      title: 'a.go',
      diff: '-old\n+new\n',
    })
    const btn = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.includes('复制输出'))
    expect(btn).toBeTruthy() // write 卡默认展开，无需点开
    ;(btn as HTMLButtonElement).click()
    await nextTick()
    expect(writeText).toHaveBeenCalledWith('-old\n+new\n')
  })

  // 0.0.12：read 卡的主标签就是路径——直接提供"打开"（用系统默认程序）
  it('read 卡提供打开文件', async () => {
    const el = mountCard({
      role: 'tool',
      content: 'package app',
      toolName: 'fs',
      status: 'success',
      op: 'read',
      title: 'internal/app/handler.go',
    })
    ;(el.querySelector('button') as HTMLButtonElement).click()
    await nextTick()
    const openBtn = Array.from(el.querySelectorAll('button')).find((b) => b.textContent?.trim() === '打开')
    expect(openBtn).toBeTruthy()
    ;(openBtn as HTMLButtonElement).click()
    await nextTick()
    expect(openCalls.opened).toEqual([{ sid: '', path: 'internal/app/handler.go', line: 0 }])
  })

  // 第 8 批：非 diff 输出（go build / go test 报错行）里的 path:line 也可点开文件
  it('命令输出里的 path:line 可点开文件', async () => {
    const el = mountCard({
      role: 'tool',
      content: 'internal/app/x.go:120: undefined: foo\nplain sentence\nmain.go:7:3: syntax error',
      toolName: 'shell',
      status: 'error',
      op: 'exec',
      title: 'go build ./...',
    })
    ;(el.querySelector('button') as HTMLButtonElement).click() // 展开
    await nextTick()
    const links = el.querySelectorAll('button[title^="打开 "]')
    expect(links).toHaveLength(2) // 两句报错行各一个可点路径；普通句子不成链接
    ;(links[0] as HTMLButtonElement).click()
    await nextTick()
    expect(openCalls.opened).toEqual([{ sid: '', path: 'internal/app/x.go', line: 120 }])
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
