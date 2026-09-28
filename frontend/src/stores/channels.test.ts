import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

// 用 vi.hoisted：vi.mock 会被提升到文件顶部，普通变量在工厂执行时会处于 TDZ。
const h = vi.hoisted(() => ({
  calls: [] as string[],
  list: { channels: [] as unknown[], activeId: '' },
  failAdd: false,
  failDiscover: false,
  // 0.2.19：渠道测试与凭证管理
  failTest: false,
  testResult: null as unknown,
  creds: [] as { index: number; preview: string; enabled: boolean }[],
  credCalls: [] as { id: string; index: number; enabled: boolean }[],
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
      TestChannel: async (id: string) => {
        h.calls.push('TestChannel')
        if (h.failTest) throw new Error(`渠道不存在：${id}`)
        return h.testResult
      },
      ListCredentials: async () => {
        h.calls.push('ListCredentials')
        return h.creds
      },
      SetCredentialEnabled: async (id: string, index: number, enabled: boolean) => {
        h.calls.push('SetCredentialEnabled')
        h.credCalls.push({ id, index, enabled })
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
    h.failTest = false
    h.testResult = null
    h.creds = []
    h.credCalls.length = 0
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

  // 测试结果按渠道留存：列表行内"✓ 42ms · 模型 · 回显"的数据源
  it('测试结果按渠道留存', async () => {
    const store = useChannelStore()
    h.testResult = { ok: true, ms: 42, model: 'm1', reply: 'pong' }
    await store.test('c1')
    expect(store.testResults.c1?.ok).toBe(true)
    expect(store.testResults.c1?.ms).toBe(42)
    expect(store.testing).toBe('')
  })

  // 机制错误（渠道不存在等）走 error 条；业务失败（上游 500）留在结果里——两类错误出口不同
  it('测试机制错误走 error 条且不写入结果', async () => {
    const store = useChannelStore()
    h.failTest = true
    await store.test('c1')
    expect(store.error).toContain('渠道不存在')
    expect(store.testResults.c1).toBeUndefined()
    expect(store.testing).toBe('')
  })

  // 凭证启停后必须刷新凭证视图与渠道列表（渠道状态联动：全禁→自动禁用、恢复→启用）
  it('凭证启停放行并刷新凭证与渠道列表', async () => {
    const store = useChannelStore()
    h.creds = [
      { index: 0, preview: 'sk-a…z', enabled: true },
      { index: 1, preview: '••••', enabled: false },
    ]
    await store.loadCredentials('c1')
    expect(store.credChannel).toBe('c1')
    expect(store.credentials.length).toBe(2)

    h.calls.length = 0
    await store.setCredentialEnabled('c1', 1, true)
    expect(h.credCalls).toEqual([{ id: 'c1', index: 1, enabled: true }])
    expect(h.calls).toEqual(['SetCredentialEnabled', 'ListCredentials', 'ListChannels'])
  })
})
