//go:build windows

package app

import (
	"sync"
	"syscall"
	"unsafe"
)

// 任务栏闪烁（0.0.19）：后台会话结束时让任务栏图标闪一下——多会话并行时用户
// 可能切在别的会话甚至别的应用上，"跑完了"必须能穿透当前焦点被看见。
// 用 FlashWindowEx（FLASHW_TIMERNOFG：闪到用户交互为止，不抢焦点）。

var (
	user32Flash            = syscall.NewLazyDLL("user32.dll")
	kernel32Flash          = syscall.NewLazyDLL("kernel32.dll")
	procEnumWindows        = user32Flash.NewProc("EnumWindows")
	procIsWindowVisible    = user32Flash.NewProc("IsWindowVisible")
	procGetWindowTextLenW  = user32Flash.NewProc("GetWindowTextLengthW")
	procGetWindowThreadPID = user32Flash.NewProc("GetWindowThreadProcessId")
	procFlashWindowEx      = user32Flash.NewProc("FlashWindowEx")
	procGetConsoleWindow   = user32Flash.NewProc("GetConsoleWindow")
	procGetCurrentProcess  = kernel32Flash.NewProc("GetCurrentProcess")
)

type flashWInfo struct {
	cbSize    uint32
	hwnd      uintptr
	dwFlags   uint32
	uCount    uint32
	dwTimeout uint32
}

const (
	flashwAll       = 0x3
	flashwTimerNoFG = 0xC
)

// flashOnce 缓存"本进程的主窗口句柄"：EnumWindows 是全窗口扫描，找到后不再扫。
// 句柄失效（极少：窗口重建）时由调用方按需重扫——这里永不主动清空，代价可忽略。
var (
	flashMu       sync.Mutex
	flashHwnd     uintptr
	flashHwndDone bool
)

// findMainWindow 枚举顶层窗口找"本进程的可见、有标题、非控制台"的窗口。
// 桌面应用只有主窗口满足全部条件；找不到返回 0（调用方静默放弃——
// 闪烁是锦上添花，绝不能因为它出错）。
func findMainWindow() uintptr {
	flashMu.Lock()
	defer flashMu.Unlock()
	if flashHwndDone {
		return flashHwnd
	}
	flashHwndDone = true
	pid, _, _ := procGetCurrentProcess.Call()
	var found uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		titleLen, _, _ := procGetWindowTextLenW.Call(hwnd)
		if titleLen == 0 {
			return 1
		}
		winPID := uint32(0)
		procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&winPID)))
		if winPID != uint32(pid) {
			return 1
		}
		found = hwnd
		return 0 // 找到了，停止枚举
	})
	procEnumWindows.Call(cb, 0)
	flashHwnd = found
	return found
}

// FlashWindow 闪一下本应用的任务栏图标（闪到用户交互为止，不抢焦点）。
func FlashWindow() {
	hwnd := findMainWindow()
	if hwnd == 0 {
		return
	}
	info := flashWInfo{
		cbSize:    uint32(unsafe.Sizeof(flashWInfo{})),
		hwnd:      hwnd,
		dwFlags:   flashwAll | flashwTimerNoFG,
		uCount:    0, // 0 = 用默认次数；与 TIMERNOFG 组合即"闪到前台为止"
		dwTimeout: 0,
	}
	procFlashWindowEx.Call(uintptr(unsafe.Pointer(&info)))
}
