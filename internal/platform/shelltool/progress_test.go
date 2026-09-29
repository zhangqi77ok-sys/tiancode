package shelltool

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 0.0.07：前台命令执行中，已捕获的输出按 ≤500ms 节流推给界面（SetProgress 回调）；
// 终态只在命令结束时出现一次（模型上下文不变，仍只有最终的头加尾结果）。
func TestShellRun_ProgressBeforeTerminal(t *testing.T) {
	tool := New(Options{Root: t.TempDir(), Timeout: 30 * time.Second})

	var mu struct {
		n    int
		last string
	}
	tool.SetProgress(func(partial string) {
		mu.n++
		mu.last = partial
	})
	defer tool.SetProgress(nil)

	// 持续约 2.5s、每秒多行输出：Windows 用 ping（每秒一Reply），其余用循环
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "ping -n 3 127.0.0.1"
	} else {
		cmd = "for i in 1 2 3; do echo tick$i; sleep 1; done"
	}
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "run", "command": cmd}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("run failed: %s", res.Content)
	}

	if mu.n == 0 {
		t.Fatal("终态之前必须收到至少一次过程更新")
	}
	if strings.TrimSpace(mu.last) == "" {
		t.Fatal("过程更新必须携带非空输出")
	}
	// 过程更新必须先于（或不包含）终态注脚——它是执行中的快照
	if strings.Contains(mu.last, "[exit code") {
		t.Fatalf("过程更新不应携带终态注脚：%s", mu.last)
	}
	// 终态内容包含完整输出（进度只给界面，模型收一次最终结果）
	if !strings.Contains(res.Content, "127.0.0.1") && !strings.Contains(res.Content, "tick") {
		t.Fatalf("终态缺少命令输出：%s", res.Content)
	}
}

// schema 无关的守卫：SetProgress(nil) 后不再推送（换步清理纪律）。
func TestShellRun_ProgressCleared(t *testing.T) {
	tool := New(Options{Root: t.TempDir(), Timeout: 30 * time.Second})
	var n int
	tool.SetProgress(func(string) { n++ })
	tool.SetProgress(nil) // 清除

	cmd := "echo done"
	if runtime.GOOS == "windows" {
		cmd = "echo done"
	}
	if _, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "run", "command": cmd})); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond) // 若回调未清除，500ms 泵会触发
	if n != 0 {
		t.Fatalf("SetProgress(nil) 后不得再推送，got %d", n)
	}
}
