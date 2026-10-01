import { useToast } from './useToast'

// 剪贴板统一壳：此前 6 处各写一遍 writeText + try/catch + toast（导出气泡、消息气泡、
// 代码块、工具卡、渠道面板）。收敛到这一处后，成功/失败的反馈纪律只剩一份：
// 成功可选提示；失败必有交代（默认 error toast，onFail 可接管到表单错误位），
// 绝不静默、也绝不假装成功。

export interface CopyOptions {
  /** 成功提示（info toast）文案；不给则不提示 */
  success?: string
  /** 失败提示（error toast）文案；缺省「复制失败：剪贴板不可用」 */
  fail?: string
  /** 完全接管失败出路（如写进表单错误位）；给了就不再弹默认 toast */
  onFail?: () => void
}

export function useClipboard() {
  const { push } = useToast()

  // copy 写剪贴板并统一反馈；返回是否成功（绝大多数调用方不需要分支——反馈已处理完）。
  async function copy(text: string, opts: CopyOptions = {}): Promise<boolean> {
    try {
      await navigator.clipboard.writeText(text)
      if (opts.success) push('info', opts.success)
      return true
    } catch {
      if (opts.onFail) opts.onFail()
      else push('error', opts.fail ?? '复制失败：剪贴板不可用')
      return false
    }
  }

  return { copy }
}
