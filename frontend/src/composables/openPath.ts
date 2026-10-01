import { errText } from './errText'
import { bridge } from '../wails'

// 打开文件的统一入口（第 8 批）：搜索结果行、shell 报错行、文件详情面板全走这里，
// 避免"哪里能点、点了干什么"各自为政。
//
// 顺序：工作区配了「在这一行打开」（workspace-settings.json 的 openAtLine）就用它；
// 没配 / 行号缺失 / 命令无法执行，一律退回 OpenInDefaultApp —— 只打开文件本身，
// 绝不假装跳到了某一行。失败交给调用方 toast（绝不静默）。
export async function openPathAt(
  sessionID: string,
  path: string,
  line = 0,
  onError?: (message: string) => void,
): Promise<void> {
  const p = path.trim()
  if (!p) return
  try {
    // 后端决定走哪条：配了 openAtLine（且行号有效）就用那条命令，否则与
    // OpenInDefaultApp 逐字一致。前端不做判断，避免两处规则漂移。
    await bridge().app.OpenAtLine(sessionID, p, line)
  } catch (e) {
    onError?.(errText(e))
  }
}
