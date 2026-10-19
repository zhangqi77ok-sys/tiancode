//go:build windows

package codeintel

import (
	"os/exec"
	"syscall"
)

// hideWindow 让桌面进程拉起的检查器（go vet / tsc / python）不闪黑窗（CREATE_NO_WINDOW）。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
}
