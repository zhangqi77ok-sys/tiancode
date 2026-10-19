//go:build !windows

package app

import (
	"os/exec"
	"syscall"
)

// hideCheckConsole 在非 Windows 上让检查命令自成进程组（超时时整组终止）。
func hideCheckConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killCheckTree 终止检查命令所在进程组（子孙进程一并收掉）。
func killCheckTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
