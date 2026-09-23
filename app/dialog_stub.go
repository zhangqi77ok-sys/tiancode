//go:build !windows

package app

import "fmt"

// NotifyError 在非 Windows 平台输出到 stderr（无 MessageBox 可用）。
func NotifyError(title, text string) {
	fmt.Printf("[ERROR] %s：%s\n", title, text)
}
