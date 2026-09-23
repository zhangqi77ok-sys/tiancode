//go:build !windows

// Package main 在非 Windows 平台提供占位入口：安装器仅支持 Windows。
// 为什么保留占位：让 `go build ./...` 在任意平台通过，保证 CI 与开发机一致。
package main

import "fmt"

func main() {
	fmt.Println("tiancode installer is Windows-only; nothing to do.")
}
