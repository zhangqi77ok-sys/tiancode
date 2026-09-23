//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

// NotifyError 以系统消息框展示启动期错误，并追加写入日志文件。
// 为什么必须弹框：应用以 windowsgui 子系统构建（-H windowsgui），println 没有
// 控制台可看——"装完点开没反应"正是缺了这一环（M5 复现实证 exit=1 无提示）。
func NotifyError(title, text string) {
	appendLog(title + "：" + text)
	user32 := syscall.NewLazyDLL("user32.dll")
	proc := user32.NewProc("MessageBoxW")
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	const mbIconError = 0x00000010
	proc.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), uintptr(mbIconError))
}

// appendLog 追加一行到 %APPDATA%\tiancode\tiancode.log（排障用；失败静默，
// 日志不可写不应阻断启动错误提示本身）。
func appendLog(line string) {
	base := os.Getenv("APPDATA")
	if base == "" {
		return
	}
	dir := filepath.Join(base, "tiancode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "tiancode.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), line)
}
