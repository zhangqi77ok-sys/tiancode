// 升级退出握手（0.0.38）：安装包检测到应用在跑时，不再让用户自己去托盘退出，
// 而是经单实例通道（`tiancode.exe -upgrade-exit` 第二实例参数 → WM_COPYDATA →
// OnSecondInstanceLaunch）请求**正在运行的实例优雅退出**——空闲则置 quitting 后
// Quit（OnBeforeClose 放行、OnShutdown 正常收尾 MCP/账本），忙（任一会话有进行
// 中回合）则拒绝退出，由安装器等待超时后中止并如实告知。
//
// 为什么经单实例通道而不是自造 IPC：wails 原生单实例锁已经把"第二实例的参数
// 送到主实例"这条路修好了（Windows 下 WM_COPYDATA + os.Args[1:]），复用零新依赖、
// 零新监听面。与 0.0.31 P0 的关系：P0 封死的是"强制结束进程"，本握手是**应用
// 自己决定退不退**——忙时拒退、空闲才退，收尾路径与托盘「退出」完全一致。
package app

import (
	"syscall"
	"unsafe"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// SingleInstanceMutexName 与 main.go options.SingleInstanceLock.UniqueId 对应——
// wails Windows 实现的互斥体名 = "wails-app-" + UniqueId + "sim"（vendor
// single_instance.go）。探针必须用同一个名字。
const SingleInstanceMutexName = "wails-app-d7c9e1a4-tiancode-single-instancesim"

var (
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	procCloseHandle  = kernel32.NewProc("CloseHandle")
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
)

// PrimaryInstanceAlive 探测是否已有主实例在跑（互斥体已存在）。
// 探针句柄用完立即关闭——绝不能握着它进 wails.Run，否则 wails 自己的 CreateMutex
// 会被误判成"第二实例"（找不到主实例窗口时它可能直接再开一份 UI）。
func PrimaryInstanceAlive() bool {
	name, _ := syscall.UTF16PtrFromString(SingleInstanceMutexName)
	h, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return false // 探测失败按"没有主实例"处理：调用方静默退出，宁可放弃升级也不拉起第二份 UI
	}
	procCloseHandle.Call(h)
	return err == syscall.ERROR_ALREADY_EXISTS
}

// requestQuit 执行真正的应用退出（quitting 已置位，OnBeforeClose 会放行）。
// 包级 var：测试注入点（单测没有 wails runtime，换桩计数）。
var requestQuit = func() {
	if b := bindInstance.Load(); b != nil && b.AppCtx != nil {
		wruntime.Quit(b.AppCtx)
	}
}

// turnRunningReporter 是"有没有回合在跑"的最小接口（*app.ChatService 满足）；
// 收窄成接口让单测可以换桩，不碰 wails 与真实服务。
type turnRunningReporter interface {
	AnyTurnRunning() bool
}

// HandleUpgradeExit 处理安装器的升级退出请求（main.go 单实例回调调用）。
// 忙（任一会话有进行中回合）→ 拒绝退出：安装器等待超时后中止，用户不会丢正在
// 跑的任务；空闲 → 与托盘「退出」同一条收尾路径（quitting 置位 → Quit →
// OnBeforeClose 放行 → OnShutdown 关 MCP/账本）。
func HandleUpgradeExit(chat turnRunningReporter) {
	if chat == nil {
		return
	}
	if chat.AnyTurnRunning() {
		LogLifecycle("升级退出请求被拒绝：有会话正在进行回合（安装器将超时中止，任务不受影响）")
		return
	}
	LogLifecycle("升级退出请求已接受：优雅关闭中（安装器将继续升级）")
	quitting.Store(true)
	requestQuit()
}
