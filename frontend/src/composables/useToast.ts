import { ref } from 'vue'

// 全局通知队列：单例状态（App 只挂一个 ToastHost）。
// 为什么不用 window.alert：原生弹窗阻塞 WebView 的 JS 线程，且样式脱离应用设计体系。
export type ToastKind = 'info' | 'error'
export interface Toast {
  id: number
  kind: ToastKind
  text: string
  // 点击动作（0.0.21）：携带动作的 toast 主体可点（如"跳到等你确认的会话"），
  // 无动作的保持纯展示——宿主据此决定是否渲染指针态。
  action?: () => void
}

const TOAST_TTL_MS = 2600

const toasts = ref<Toast[]>([])
let seq = 0

export function useToast() {
  // push 后按 TTL 自动消失；错误文案同样限时（错误另有 store.error 常驻位兜底）
  function push(kind: ToastKind, text: string, action?: () => void) {
    const id = ++seq
    toasts.value.push({ id, kind, text, action })
    setTimeout(() => dismiss(id), TOAST_TTL_MS)
  }

  // 点击动作执行后随点随消（保留动作结果，不再等 TTL）
  function activate(t: Toast) {
    if (!t.action) return
    t.action()
    dismiss(t.id)
  }

  function dismiss(id: number) {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }

  return { toasts, push, activate, dismiss }
}
