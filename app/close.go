// 关窗生命周期（0.0.24 语义变更，取代 0.0.21 的"关窗确认框"）：
// 点 X / Alt+F4 / 系统关窗 = 隐藏到托盘（后台轮次继续跑，不打扰）；
// 真正退出只从托盘菜单走——"退出"置位 quitting 后，OnBeforeClose 放行本次关闭。
// 0.0.21 的确认框链路（chat:close-requested + ForceQuit）随新语义整体下线。
package app

import (
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// HideToTray 隐藏主窗口到托盘（X 按钮与 OnBeforeClose 共用同一动作）。
// 后台轮次继续跑；唤回走托盘左键或"显示主窗口"。
func (b *Bind) HideToTray() {
	wruntime.WindowHide(b.AppCtx)
}
