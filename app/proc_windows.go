//go:build windows

package app

import (
	"os/exec"
	"syscall"
)

// createNoWindow 避免从桌面进程调用控制台程序（cmd.exe）时弹出一闪而过的黑窗。
const createNoWindow = 0x08000000

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
		HideWindow:    true,
	}
}
