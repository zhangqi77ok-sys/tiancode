package app

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// 2026-10-19：检查命令超时要收整棵进程树。此前只杀直接子进程（cmd.exe），孙进程
// 还握着输出管道，CombinedOutput 一直等到它自己跑完（这里约 30s）。
func TestCheckRunner_TimeoutKillsTree(t *testing.T) {
	name, args := "sh", []string{"-c", "sleep 30"}
	if runtime.GOOS == "windows" {
		name, args = "cmd", []string{"/d", "/c", "ping -n 30 127.0.0.1"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	_, err := checkRunner(ctx, name, args, t.TempDir())
	if err == nil {
		t.Fatal("超时应返回错误")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("超时后应在数秒内收束（整棵树被终止），实际 %v", elapsed)
	}
}
