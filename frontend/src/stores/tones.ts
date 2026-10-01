import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import type { ToneEntryDTO } from '../wails'
import { bridge } from '../wails'
import { errText } from '../composables/errText'

// 语气设置（第 8 批）：固定还是自动、默认哪一条、停用了哪几条。
//
// 为什么名单由后端给：内置 50 条的 id / 名称 / 做法在 Go 常量里，注入系统提示用的
// 就是那份文本。前端再抄一份，"界面显示的做法"与"模型看到的做法"迟早不一致。
//
// 写路径纪律与扩展清单一致（0.2.27）：保存失败把后端错误**原样**带回（'' = 成功），
// 界面照实显示——"停用默认语气"这类拒绝规则只有后端一份，前端不复制一套本地校验。

export const useTonesStore = defineStore('tones', () => {
  const mode = ref<'fixed' | 'auto'>('fixed')
  const defaultId = ref('plain')
  const disabled = ref<string[]>([])
  const builtin = ref<ToneEntryDTO[]>([])
  const loaded = ref(false)
  const busy = ref(false)

  // 侧栏那一行要能一眼看出"现在按哪种语气回答"（做法太长，只显示名称）
  const label = computed(() => {
    const name = builtin.value.find((e) => e.id === defaultId.value)?.name ?? defaultId.value
    return mode.value === 'auto' ? `自动 · 默认 ${name}` : `固定 · ${name}`
  })

  // 载入设置与内置名单。返回错误文本（'' = 成功）——界面按需显示。
  async function load(): Promise<string> {
    busy.value = true
    try {
      const view = await bridge().app.GetTones()
      mode.value = view.mode === 'auto' ? 'auto' : 'fixed'
      defaultId.value = view.default || 'plain'
      disabled.value = Array.isArray(view.disabled) ? [...view.disabled] : []
      builtin.value = Array.isArray(view.builtin) ? view.builtin : []
      loaded.value = true
      return ''
    } catch (e) {
      return errText(e)
    } finally {
      busy.value = false
    }
  }

  // 保存设置：成功后本店立刻反映新值（侧栏状态同步），失败保留原值并带回错误原文。
  async function save(next: { mode: string; default: string; disabled: string[] }): Promise<string> {
    busy.value = true
    try {
      await bridge().app.SaveTones(next)
      mode.value = next.mode === 'auto' ? 'auto' : 'fixed'
      defaultId.value = next.default
      disabled.value = [...next.disabled]
      return ''
    } catch (e) {
      return errText(e)
    } finally {
      busy.value = false
    }
  }

  return { mode, defaultId, disabled, builtin, loaded, busy, label, load, save }
})
