import { defineStore } from 'pinia'
import { ref } from 'vue'
import { bridge, type ChannelDTO, type ChannelInput, type PresetDTO } from '../wails'

// 渠道管理状态。设计要点：
// - 错误只保留一条 message（单面板场景，堆栈式提示反而干扰）；
// - 每个写操作后重新拉取列表，UI 始终以"后端已落盘的状态"为准（不做乐观更新，
//   避免"界面说改好了、实际没落盘"的假象——这正是旧实现丢配置的观感来源）。
export const useChannelStore = defineStore('channels', () => {
  const list = ref<ChannelDTO[]>([])
  const activeId = ref('')
  const presets = ref<PresetDTO[]>([])
  const models = ref<string[]>([])
  const loading = ref(false)
  const busy = ref(false)
  const error = ref('')

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

  return { list, activeId, presets, models, loading, busy, error, load, loadPresets, save, remove, activate, discover, clearError }
})
