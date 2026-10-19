//go:build !windows

package codeintel

import "os/exec"

// hideWindow 在非 Windows 平台无事可做。
func hideWindow(*exec.Cmd) {}
