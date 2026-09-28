//go:build !windows

package gittool

import "os/exec"

func hideConsole(cmd *exec.Cmd) {}
