//go:build !windows

package app

// FlashWindow 在非 Windows 平台是空操作（无任务栏闪烁概念）。
func FlashWindow() {}
