//go:build !windows

package searchtool

import "os/exec"

// hideConsole 在非 Windows 平台无事可做。
func hideConsole(*exec.Cmd) {}
