//go:build !windows

package app

import "os/exec"

func hideCheckConsole(cmd *exec.Cmd) {}
