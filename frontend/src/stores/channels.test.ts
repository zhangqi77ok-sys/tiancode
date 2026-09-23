import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// 用 vi.hoisted：vi.mock 会被提升到文件顶部，普通变量在工厂执行时会处于 TDZ。
const h = vi.hoisted(() => ({
  calls: [] as string[],
  list: { channels: [] as unknown[], activeId: '' },
  failAdd: false,
  failDiscover: false,
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      ListChannels: async () => {
        h.calls.push('ListChannels')
        return h.list
      },
      ChannelPresets: async () => [],
      AddChannel: async (input: unknown) => {
        h.calls.push('AddChannel')
        if (h.failAdd) throw new Error('渠道缺少字段：baseUrl')
        return { ...(input as object), id: 'new' }
      },
      UpdateChannel: async () => h.calls.push('UpdateChannel'),
      DeleteChannel: async () => h.calls.push('DeleteChannel'),
      SetActiveChannel: async () => h.calls.push('SetActiveChannel'),
      DiscoverModels: async () => {
        h.calls.push('DiscoverModels')
        if (h.failDiscover) throw new Error('上游返回 HTTP 500')
        return ['a-model', 'z-model']
      },
    },
    runtime: { EventsOn: () => {} },
  }),
}))

const { useChannelStore } = await import('./channels')

const input = { id: '', name: '主', protocol: 'openai', baseUrl: 'https://gw/v1', model: 'm', apiKey: 'sk' }

describe('channels store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    h.calls.length = 0
    h.list = { channels: [], activeId: '' }
    h.failAdd = false
    h.failDiscover = false
  })

  // 写后必须重新拉取：界面只反映后端已落盘状态（不做乐观更新）
  it('save 后重新拉取列表', async () => {
    const store = useChannelStore()
    await store.save(input)
    expect(h.calls).toEqual(['AddChannel', 'ListChannels'])
    expect(store.error).toBe('')
  })

  // 失败必须可见：错误进 store，且不误报成功
  it('save 失败时写入错误且不再拉取', async () => {
    h.failAdd = true
    const store = useChannelStore()
    await store.save(input)
    expect(store.error).toContain('baseUrl')
    expect(h.calls).toEqual(['AddChannel'])
  })

  // 切换默认渠道后列表刷新（顶栏渠道名随之更新）
  it('activate 后重新拉取', async () => {
    const store = useChannelStore()
    await store.activate('c1')
    expect(h.calls).toEqual(['SetActiveChannel', 'ListChannels'])
  })

  // 发现失败只提示，不清空已选模型（C-CH-4 的 UI 侧保证）
  it('模型发现失败不清空已有模型', async () => {
    const store = useChannelStore()
    store.models = ['keep-me']
    h.failDiscover = true
    await store.discover(input)
    expect(store.error).toContain('500')
    expect(store.models).toEqual(['keep-me'])
  })

  it('模型发现成功后写入模型列表', async () => {
    const store = useChannelStore()
    await store.discover(input)
    expect(store.models).toEqual(['a-model', 'z-model'])
    expect(store.error).toBe('')
  })
})
