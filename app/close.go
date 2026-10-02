// 关窗生命周期用例（0.0.21）：后台有轮次在跑时拦截关窗，交前端确认。
package app

import (
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ForceQuit 真正退出应用：仅供前端在「关窗确认」框中确认后调用。
// 普通关窗（无运行中会话）走系统路径，不经这里。
func (b *Bind) ForceQuit() {
	wruntime.Quit(b.AppCtx)
}
