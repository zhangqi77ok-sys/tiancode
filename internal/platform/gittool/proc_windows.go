//go:build windows

package gittool

import (
	"os/exec"
	"syscall"
)

// createNoWindow 避免从桌面进程调用 git.exe 时弹出命令行窗口。
const createNoWindow = 0x08000000

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
		HideWindow:    true,
	}
}
