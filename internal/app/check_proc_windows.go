//go:build windows

package app

import (
	"os/exec"
	"syscall"
)

// createNoWindow 避免检查命令从桌面进程拉起时弹出命令行窗口。
const createNoWindow = 0x08000000

func hideCheckConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
		HideWindow:    true,
	}
}
