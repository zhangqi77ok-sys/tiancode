//go:build windows

package mcpclient

import (
	"os/exec"
	"testing"
)

func TestHideConsole(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit")
	hideConsole(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatal("启动命令未隐藏控制台窗口")
	}
}
