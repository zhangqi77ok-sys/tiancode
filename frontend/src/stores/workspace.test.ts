import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// workspace store：refresh 是启动链与切工作区的公共路径——失败必须自吞
// （此前上抛，AppHeader 挂载时裸 await 会变未处理 rejection 并打断后续初始化）。

const h = vi.hoisted(() => ({
  workspace: '' as string,
  failGet: false,
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      GetWorkspace: async () => {
        if (h.failGet) throw new Error('桥接断开')
        return h.workspace
      },
      SetWorkspace: async () => {},
      PickWorkspace: async () => '',
    },
    runtime: { EventsOn: () => {} },
  }),
}))

const { useWorkspaceStore } = await import('./workspace')
const { useToast } = await import('../composables/useToast')

describe('workspace store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    h.workspace = ''
    h.failGet = false
  })

  it('refresh 成功：path 取后端返回', async () => {
    h.workspace = 'D:\\work\\proj'
    const ws = useWorkspaceStore()
    await ws.refresh()
    expect(ws.path).toBe('D:\\work\\proj')
  })

  it('refresh 失败：不上抛（不产生未处理 rejection）、toast 可见、path 保持原值', async () => {
    const ws = useWorkspaceStore()
    ws.path = 'D:\\old'
    h.failGet = true
    await expect(ws.refresh()).resolves.toBeUndefined()
    expect(ws.path).toBe('D:\\old')
    const { toasts } = useToast()
    expect(toasts.value.some((t) => t.kind === 'error' && t.text.includes('读取工作区失败'))).toBe(true)
  })
})
