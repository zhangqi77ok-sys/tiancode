import { ref } from 'vue'

// 全局通知队列：单例状态（App 只挂一个 ToastHost）。
// 为什么不用 window.alert：原生弹窗阻塞 WebView 的 JS 线程，且样式脱离应用设计体系。
export type ToastKind = 'info' | 'error'
export interface Toast {
  id: number
  kind: ToastKind
  text: string
}

const TOAST_TTL_MS = 2600

const toasts = ref<Toast[]>([])
let seq = 0

export function useToast() {
  // push 后按 TTL 自动消失；错误文案同样限时（错误另有 store.error 常驻位兜底）
  function push(kind: ToastKind, text: string) {
    const id = ++seq
    toasts.value.push({ id, kind, text })
    setTimeout(() => dismiss(id), TOAST_TTL_MS)
  }

  function dismiss(id: number) {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }

  return { toasts, push, dismiss }
}
