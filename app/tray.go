// 系统托盘（0.0.24）：最小化常驻 + 真正退出的唯一入口。
//
// 语义（用户裁决，登记于 docs/PENDING）：
//   - 点 X / Alt+F4 / 系统关窗 = 隐藏到托盘（后台轮次继续跑）；
//   - 真正退出只从托盘菜单走（菜单"退出"置位 quitting 后放行 OnBeforeClose）；
//   - 左键单击 = 唤回主窗口；右键 = 菜单（energye/systray 的默认分发）。
//
// 为什么选 energye/systray：Wails v2 官方 2.x 后期已移除托盘（实测 v2.9.2 与
// v2.16 均无）；该 fork 纯 Go + Win32 隐藏窗口消息循环，Run 在自己的 goroutine
// 上 LockOSThread，与 Wails 主循环互不抢线程。Apache-2.0，vendor 入库保持
// "离线可构建"纪律。
package app

import (
	_ "embed"
	"sync/atomic"

	"github.com/energye/systray"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed tray_icon.ico
var trayIconICO []byte

// quitting 是"真退出"标记：托盘菜单退出置位后，OnBeforeClose 放行本次关闭；
// 其余关窗路径一律转隐藏到托盘。Quit 只能从这里来（Close 复用同一标记）。
var quitting atomic.Bool

// Quitting 报告本次关闭是否来自托盘"退出"（main.go 的 OnBeforeClose 判据）。
func Quitting() bool { return quitting.Load() }

// StartTray 启动托盘（在 OnStartup 里调——AppCtx 已就绪，菜单回调才有 ctx 可用）。
// 刻意做成包级函数而非 Bind 方法：它不是前端可调的 IPC 端点（bind_test 的表面
// 清单按方法名收敛，生命周期入口混进去会变成"前端能拉起托盘"的假契约）。
// systray.Run 会锁住调用 goroutine 的线程跑 Win32 消息循环，必须另起 goroutine。
func StartTray(b *Bind) {
	go func() {
		systray.Run(b.onTrayReady, func() {})
	}()
}

func (b *Bind) onTrayReady() {
	systray.SetIcon(trayIconICO)
	systray.SetTooltip("tiancode — 后台对话继续运行中")
	mShow := systray.AddMenuItem("显示主窗口", "唤回主窗口（左键单击图标同效）")
	mNew := systray.AddMenuItem("新建对话", "唤回主窗口并开一个草稿")
	mUpdate := systray.AddMenuItem("检查更新", "查看 GitHub 最新版本")
	mLogs := systray.AddMenuItem("打开日志目录", "排障用（按天轮转，保留 7 天）")
	systray.AddSeparator()
	mExit := systray.AddMenuItem("退出", "真正退出应用（进行中的后台轮次将被中断）")

	systray.SetOnClick(func(_ systray.IMenu) { b.showMainWindow() })
	mShow.Click(b.showMainWindow)
	mNew.Click(func() {
		b.showMainWindow()
		// 新建对话是前端状态（草稿/侧栏），后端只发事件不越权改 store
		wruntime.EventsEmit(b.AppCtx, "tray:new-session")
	})
	mUpdate.Click(func() {
		b.showMainWindow()
		// 更新流程的确认框在前端（AppHeader），这里只负责唤回并递事件
		wruntime.EventsEmit(b.AppCtx, "tray:check-update")
	})
	mLogs.Click(func() { b.OpenLogDir() })
	mExit.Click(func() {
		quitting.Store(true)
		systray.Quit()
		// 给托盘移除留一点时间，再走正常退出（OnBeforeClose 看到 quitting 放行）
		wruntime.Quit(b.AppCtx)
	})
}

// showMainWindow 唤回主窗口（托盘左键 / "显示主窗口" / "新建对话"共用）。
// WindowUnminimise 处理最小化态，WindowShow 处理隐藏态；两个都调顺序无敏感。
func (b *Bind) showMainWindow() {
	if ctx := b.AppCtx; ctx != nil {
		wruntime.WindowUnminimise(ctx)
		wruntime.WindowShow(ctx)
	}
}

// RestoreMainWindow 供 main.go 的单实例锁回调用（第二实例启动时唤起已有窗口）。
// 为什么不复用 Bind.showMainWindow：那一刻 Bind 可能还没装配好 AppCtx（OnStartup
// 未跑），而单实例锁在 Wails 启动早期触发——AppCtx 为 nil 时这里什么都不做，
// 宁可"唤不醒"也不要在缺 ctx 的情况下调 runtime（会 panic）。
func RestoreMainWindow() {
	if quitting.Load() {
		return // 正在退出：别把窗口拽回来
	}
	if b := bindInstance.Load(); b != nil {
		b.showMainWindow()
	}
}

// bindInstance 记录当前运行的 Bind（单实例锁回调是包级函数，没有 receiver）。
var bindInstance atomic.Pointer[Bind]

// SetActiveBind 登记运行中的 Bind（main.go 在装配后调一次）。
func SetActiveBind(b *Bind) { bindInstance.Store(b) }
