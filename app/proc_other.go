//go:build !windows

package app

import "os/exec"

// hideConsole 在非 Windows 平台是空操作（无控制台窗口概念）。
func hideConsole(cmd *exec.Cmd) {}
