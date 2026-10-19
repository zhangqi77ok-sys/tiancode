import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createApp, nextTick, type App } from 'vue'
import { useChatStore } from '../stores/chat'
import { useDialogs } from '../composables/useDialogs'

const h = vi.hoisted(() => ({
  approvableTools: ['shell', 'browser', 'mcp', 'ext_manage', 'fs'],
  approvalPolicy: ['shell', 'browser'],
  defaultPolicy: ['shell', 'ext_manage', 'mcp', 'browser'],
  sessionAllows: ['git status', 'npm test'],
  setPolicyCalls: [] as string[][],
  revokeCalls: [] as { sessionId: string; prefix: string }[],
  revokeFails: false,
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ApprovableTools: async () => [...h.approvableTools],
      ApprovalPolicy: async () => [...h.approvalPolicy],
      DefaultApprovalPolicy: async () => [...h.defaultPolicy],
      SetApprovalPolicy: async (tools: string[]) => {
        h.setPolicyCalls.push([...tools])
        h.approvalPolicy = [...tools]
      },
      SessionShellAllow: async (_sessionId: string) => [...h.sessionAllows],
      RevokeSessionShellAllow: async (sessionId: string, prefix: string) => {
        if (h.revokeFails) {
          throw new Error('撤销失败：该命令前缀已被锁定')
        }
        h.revokeCalls.push({ sessionId, prefix })
        h.sessionAllows = h.sessionAllows.filter((p) => p !== prefix)
      },
    },
    runtime: { EventsOn: () => {} },
  }),
}))

const ApprovalSettings = (await import('./ApprovalSettings.vue')).default

let app: App | null = null

async function mountPanel() {
  const host = document.createElement('div')
  host.id = 'panel-host'
  document.body.appendChild(host)
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useChatStore()
  store.sessionId = 's-test'
  app = createApp(ApprovalSettings)
  app.use(pinia)
  app.mount(host)
  await new Promise((r) => setTimeout(r, 0))
  await nextTick()
}

describe('ApprovalSettings 审批与权限设置面板', () => {
  beforeEach(() => {
    h.approvableTools = ['shell', 'browser', 'mcp', 'ext_manage', 'fs']
    h.approvalPolicy = ['shell', 'browser']
    h.defaultPolicy = ['shell', 'ext_manage', 'mcp', 'browser']
    h.sessionAllows = ['git status', 'npm test']
    h.setPolicyCalls = []
    h.revokeCalls = []
    h.revokeFails = false
  })

  afterEach(() => {
    if (app) {
      app.unmount()
      app = null
    }
    document.body.innerHTML = ''
  })

  it('勾选状态与 ApprovalPolicy() 一致；改勾选后保存，SetApprovalPolicy 收到勾选项（顺序以 ApprovableTools() 为准）', async () => {
    await mountPanel()

    const checkboxes = Array.from(document.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    expect(checkboxes).toHaveLength(5)

    // 勾选状态与 ApprovalPolicy() = ['shell', 'browser'] 一致
    const checkedValues = checkboxes.filter((c) => c.checked).map((c) => c.value)
    expect(checkedValues).toEqual(['shell', 'browser'])

    // 点击切换：取消 browser，勾选 mcp 和 fs
    const browserCb = checkboxes.find((c) => c.value === 'browser')!
    const mcpCb = checkboxes.find((c) => c.value === 'mcp')!
    const fsCb = checkboxes.find((c) => c.value === 'fs')!

    browserCb.click()
    mcpCb.click()
    fsCb.click()
    await nextTick()

    // 点击保存
    const saveBtn = Array.from(document.querySelectorAll('button')).find((b) => b.textContent?.includes('保存'))!
    saveBtn.click()
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    expect(h.setPolicyCalls).toHaveLength(1)
    // 顺序以 ApprovableTools() 为准: shell, mcp, fs
    expect(h.setPolicyCalls[0]).toEqual(['shell', 'mcp', 'fs'])
    expect(useChatStore().approvalPolicy).toEqual(['shell', 'mcp', 'fs'])
  })

  it('「恢复默认」后保存，参数等于 DefaultApprovalPolicy() 的 mock 返回值', async () => {
    await mountPanel()

    const defaultBtn = Array.from(document.querySelectorAll('button')).find((b) => b.textContent?.includes('恢复默认'))!
    defaultBtn.click()
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    // 此时勾选应当变为 defaultPolicy
    const checkboxes = Array.from(document.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    const checkedValues = checkboxes.filter((c) => c.checked).map((c) => c.value)
    expect(checkedValues.sort()).toEqual([...h.defaultPolicy].sort())

    // 点击保存
    const saveBtn = Array.from(document.querySelectorAll('button')).find((b) => b.textContent?.includes('保存'))!
    saveBtn.click()
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    expect(h.setPolicyCalls).toHaveLength(1)
    expect(h.setPolicyCalls[0]).toEqual(['shell', 'browser', 'mcp', 'ext_manage'])
  })

  it('全部取消勾选保存时先出确认；取消确认则 SetApprovalPolicy 未被调用', async () => {
    await mountPanel()

    const checkboxes = Array.from(document.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    // 取消所有勾选项
    for (const cb of checkboxes) {
      if (cb.checked) {
        cb.click()
      }
    }
    await nextTick()

    const saveBtn = Array.from(document.querySelectorAll('button')).find((b) => b.textContent?.includes('保存'))!
    const dialogs = useDialogs()

    // 点击保存：触发 confirm
    saveBtn.click()
    await nextTick()

    // 用户在确认框中取消
    dialogs.resolveConfirm(false)
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    // 取消确认则 SetApprovalPolicy 未被调用
    expect(h.setPolicyCalls).toEqual([])

    // 再次点击保存，这次用户确认
    saveBtn.click()
    await nextTick()
    dialogs.resolveConfirm(true)
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    expect(h.setPolicyCalls).toEqual([[]])
    expect(useChatStore().approvalPolicy).toEqual([])
  })

  it('撤销前缀 → RevokeSessionShellAllow(当前会话, 前缀) 被调用，列表刷新；失败时错误可见、列表不变', async () => {
    await mountPanel()

    // 初始列出两条已允许前缀
    expect(document.body.textContent).toContain('git status')
    expect(document.body.textContent).toContain('npm test')

    // 撤销一条 'git status'
    const revokeBtns = Array.from(document.querySelectorAll('button')).filter((b) => b.textContent?.includes('撤销'))
    expect(revokeBtns).toHaveLength(2)
    revokeBtns[0].click()
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    expect(h.revokeCalls).toEqual([{ sessionId: 's-test', prefix: 'git status' }])
    expect(document.body.textContent).not.toContain('git status')
    expect(document.body.textContent).toContain('npm test')

    // 模拟撤销失败
    h.revokeFails = true
    const remainingRevokeBtn = Array.from(document.querySelectorAll('button')).find((b) => b.textContent?.includes('撤销'))!
    remainingRevokeBtn.click()
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    // 错误在面板中可见，列表未变
    expect(document.body.textContent).toContain('撤销失败：该命令前缀已被锁定')
    expect(document.body.textContent).toContain('npm test')
  })

  it('保存后顶栏开关状态正确（策略为空=关，非空=开）', async () => {
    await mountPanel()
    const store = useChatStore()

    // 1. 保存为非空策略
    const saveBtn = Array.from(document.querySelectorAll('button')).find((b) => b.textContent?.includes('保存'))!
    saveBtn.click()
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    expect(store.approvalPolicy.length).toBeGreaterThan(0)

    // 2. 全部取消并保存为空策略
    const checkboxes = Array.from(document.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    for (const cb of checkboxes) {
      if (cb.checked) cb.click()
    }
    await nextTick()

    saveBtn.click()
    await nextTick()
    useDialogs().resolveConfirm(true)
    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    expect(store.approvalPolicy).toEqual([])
  })
})
