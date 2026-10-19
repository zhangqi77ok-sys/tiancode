//go:build windows

package searchtool

import (
	"os/exec"
	"syscall"
)

// hideConsole 让桌面进程拉起的 git 不闪黑窗（CREATE_NO_WINDOW）。
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
}
