import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import {
  bridge,
  type ChannelDTO,
  type ChannelInput,
  type CredentialDTO,
  type PresetDTO,
  type TestResultDTO,
} from '../wails'

// 渠道管理状态。设计要点：
// - 错误只保留一条 message（单面板场景，堆栈式提示反而干扰）；
// - 每个写操作后重新拉取列表，UI 始终以"后端已落盘的状态"为准（不做乐观更新，
//   避免"界面说改好了、实际没落盘"的假象——这正是旧实现丢配置的观感来源）；
// - 测试结果按渠道留存（内存态）：列表行内显示最近一次延迟/错误，关面板即失效。
export const useChannelStore = defineStore('channels', () => {
  const list = ref<ChannelDTO[]>([])
  const activeId = ref('')
  const presets = ref<PresetDTO[]>([])
  const models = ref<string[]>([])
  const loading = ref(false)
  const busy = ref(false)
  const error = ref('')

  // 测试：正在测试的渠道 id + 每渠道最近结果
  const testing = ref('')
  const testResults = ref<Record<string, TestResultDTO>>({})

  // 凭证管理：当前展开的渠道 id + 其凭证视图
  const credChannel = ref('')
  const credentials = ref<CredentialDTO[]>([])

  function fail(e: unknown) {
    error.value = String(e instanceof Error ? e.message : e)
  }

  async function load() {
    loading.value = true
    error.value = ''
    try {
      const res = await bridge().app.ListChannels()
      list.value = res?.channels ?? []
      activeId.value = res?.activeId ?? ''
    } catch (e) {
      fail(e)
    } finally {
      loading.value = false
    }
  }

  async function loadPresets() {
    try {
      presets.value = (await bridge().app.ChannelPresets()) ?? []
    } catch (e) {
      fail(e)
    }
  }

  // 保存：无 id 为新增，有 id 为更新；成功后重新拉取列表
  async function save(input: ChannelInput) {
    busy.value = true
    error.value = ''
    try {
      if (input.id) await bridge().app.UpdateChannel(input)
      else await bridge().app.AddChannel(input)
      await load()
    } catch (e) {
      fail(e)
    } finally {
      busy.value = false
    }
  }

  async function remove(id: string) {
    busy.value = true
    error.value = ''
    try {
      await bridge().app.DeleteChannel(id)
      await load()
    } catch (e) {
      fail(e)
    } finally {
      busy.value = false
    }
  }

  async function activate(id: string) {
    busy.value = true
    error.value = ''
    try {
      await bridge().app.SetActiveChannel(id)
      await load()
    } catch (e) {
      fail(e)
    } finally {
      busy.value = false
    }
  }

  // 测试渠道连通性（走真实链路）：结果留在 testResults 供行内显示；
  // 机制错误（渠道不存在等）走 error 条，业务失败（上游 500/超时）走结果卡
  async function test(id: string) {
    testing.value = id
    error.value = ''
    try {
      const res = await bridge().app.TestChannel(id)
      if (res) testResults.value = { ...testResults.value, [id]: res }
    } catch (e) {
      fail(e)
    } finally {
      testing.value = ''
    }
  }

  // 打开凭证管理视图（脱敏列表）
  async function loadCredentials(id: string) {
    credChannel.value = id
    error.value = ''
    try {
      credentials.value = (await bridge().app.ListCredentials(id)) ?? []
    } catch (e) {
      fail(e)
    }
  }

  // 启用/禁用单条凭证；渠道状态可能联动（全禁 → 自动禁用；恢复 → 启用），
  // 因此凭证刷新后必须重新拉渠道列表（否则列表状态与后端不一致）
  async function setCredentialEnabled(id: string, index: number, enabled: boolean) {
    busy.value = true
    error.value = ''
    try {
      await bridge().app.SetCredentialEnabled(id, index, enabled)
      credentials.value = (await bridge().app.ListCredentials(id)) ?? []
      await load()
    } catch (e) {
      fail(e)
    } finally {
      busy.value = false
    }
  }

  // 同步模型：纯读取；失败只提示，不清空已选模型（避免"点一下清空"的挫败感）
  async function discover(input: ChannelInput) {
    busy.value = true
    error.value = ''
    try {
      const got = await bridge().app.DiscoverModels(input)
      models.value = got ?? []
      if (!models.value.length) error.value = '上游未返回任何模型，请检查地址与密钥'
    } catch (e) {
      fail(e)
    } finally {
      busy.value = false
    }
  }

  function clearError() {
    error.value = ''
  }

  // 主界面要显示的是模型，不是渠道后台字段。缺省取声明列表的第一项（与运行时 DefaultModel 一致）。
  const activeChannel = computed(() => list.value.find((c) => c.active) ?? null)
  const activeModel = computed(() => {
    const ch = activeChannel.value
    if (!ch) return ''
    const listed = (ch.models ?? []).map((m) => m.trim()).filter(Boolean)
    return listed[0] || ch.model || ''
  })

  return {
    list,
    activeId,
    activeChannel,
    activeModel,
    presets,
    models,
    loading,
    busy,
    error,
    testing,
    testResults,
    credChannel,
    credentials,
    load,
    loadPresets,
    save,
    remove,
    activate,
    test,
    loadCredentials,
    setCredentialEnabled,
    discover,
    clearError,
  }
})
