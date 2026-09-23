//go:build !windows

package shelltool

import (
	"os/exec"
	"syscall"
)

// setupProcAttr 设置平台进程属性（Unix：独立进程组，便于按组终止）。
func setupProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killPIDTree 终止整棵进程组（负数 pid 表示进程组）。兜底单进程终止。
func killPIDTree(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		return syscall.Kill(pid, syscall.SIGKILL)
	}
	return nil
}
