//go:build windows

package main

import (
	"log"
	"os"
	"testing"
)

// TestMain 把 APPDATA 重定向到临时目录：安装器的 warn/appendSetupLog 会往
// %APPDATA%\tiancode\setup.log 落盘，测试（如 TestDirGuard 的回滚失败路径）
// 触发时若无此隔离，会把测试警告写进用户真实的安装日志，干扰排障。
func TestMain(m *testing.M) {
	fakeAppData, err := os.MkdirTemp("", "tiancode-setup-test-appdata")
	if err != nil {
		log.Fatalf("创建测试 APPDATA 失败：%v", err)
	}
	defer os.RemoveAll(fakeAppData)

	oldAppData, hadAppData := os.LookupEnv("APPDATA")
	os.Setenv("APPDATA", fakeAppData)
	code := m.Run()
	if hadAppData {
		os.Setenv("APPDATA", oldAppData)
	} else {
		os.Unsetenv("APPDATA")
	}
	os.Exit(code)
}
