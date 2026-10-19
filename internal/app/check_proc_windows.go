//go:build windows

package app

import (
	"os/exec"
	"strconv"
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

// killCheckTree 终止检查命令的整棵进程树（2026-10-19）：exec.CommandContext 默认只杀
// 直接子进程——`npm run build` 超时后 node 子进程仍在跑（占 CPU、锁文件），下一次
// 检查与之赛跑。与 shelltool 同一做法（taskkill /T）。
func killCheckTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	k := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	k.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
	return k.Run()
}
