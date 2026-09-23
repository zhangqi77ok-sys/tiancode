//go:build windows

package shelltool

import (
	"os/exec"
	"strconv"
	"syscall"
)

// createNoWindow 是 Windows 的 CREATE_NO_WINDOW 创建标志。
// 为什么必须：不加会在每次执行命令时弹出黑色控制台窗口（legacy terminal_tool.go:223 的教训）。
const createNoWindow = 0x08000000

// setupProcAttr 设置平台进程属性（Windows：隐藏窗口）。
func setupProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
		HideWindow:    true,
	}
}

// killPIDTree 终止整棵进程树：cmd 会派生孙进程，必须 /T 连子带孙一起杀。
func killPIDTree(pid int) error {
	k := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	k.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
	return k.Run()
}
