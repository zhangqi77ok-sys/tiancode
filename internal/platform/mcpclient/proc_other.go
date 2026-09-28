//go:build !windows

package mcpclient

import (
	"os/exec"
	"syscall"
)

func hideConsole(cmd *exec.Cmd) {}

// killProcessTree 非 Windows：直接杀进程本身（npx 的批处理链只在 Windows 上出现）。
func killProcessTree(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}
