import { ref } from 'vue'

// 应用级确认/输入对话框：单例状态 + Promise 化调用。
// 取代 window.confirm/prompt：原生弹窗会阻塞 WebView 的 JS 线程，
// 且样式与设计体系完全脱节（这是 UI 重建清单中的明确项）。
export interface ConfirmOptions {
  title: string
  message: string
  confirmText?: string
  danger?: boolean
}
export interface PromptOptions {
  title: string
  message?: string
  value?: string
  placeholder?: string
  maxlength?: number
}

interface ConfirmState extends ConfirmOptions {
  resolve: (ok: boolean) => void
}
interface PromptState extends PromptOptions {
  resolve: (value: string | null) => void
}

const confirmState = ref<ConfirmState | null>(null)
const promptState = ref<PromptState | null>(null)

export function useDialogs() {
  // 已有对话框在等答案时再次调用：前一个按取消收场，绝不叠加悬挂的 Promise
  function confirm(opts: ConfirmOptions): Promise<boolean> {
    confirmState.value?.resolve(false)
    promptState.value?.resolve(null)
    return new Promise((resolve) => {
      confirmState.value = { ...opts, resolve }
    })
  }

  function prompt(opts: PromptOptions): Promise<string | null> {
    confirmState.value?.resolve(false)
    promptState.value?.resolve(null)
    return new Promise((resolve) => {
      promptState.value = { ...opts, resolve }
    })
  }

  function resolveConfirm(ok: boolean) {
    confirmState.value?.resolve(ok)
    confirmState.value = null
  }

  function resolvePrompt(value: string | null) {
    promptState.value?.resolve(value)
    promptState.value = null
  }

  return { confirmState, promptState, confirm, prompt, resolveConfirm, resolvePrompt }
}
