package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tiancode/internal/platform/shelltool"
)

// BgTasksSnapshot 用例：shell 工具内存任务表 → 视图快照（右栏「任务」tab 数据源）。
// 经真实 shelltool 全链路（bg_start → 轮询快照 → 结束态），不造假实现——
// 任务运行态只有从工具实例里读出来才是真相。
func TestChatService_BgTasksSnapshot(t *testing.T) {
	s, err := NewChatService(Config{
		DataDir:      t.TempDir(),
		WorkDir:      t.TempDir(),
		ChannelsPath: filepath.Join(t.TempDir(), "channels.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() }) // 会话工具集随服务收掉：不留孤儿后台进程

	root := t.TempDir()
	sh := shelltool.New(shelltool.Options{Root: root})
	// browser tab 与真实装配同构（ensureSessionTools 恒构造；惰性、不拉进程）——
	// 手搓的半空 sessionTools 在 Close 收尾时会把 typed-nil 传进 closeToolIfCloser
	st := &sessionTools{root: root, shell: sh}
	st.browser = s.browser.NewTab("s-bg")
	s.sessTools["s-bg"] = st

	// 未知会话（没有工具集）：空表而非错误
	if got := s.BgTasksSnapshot("s-none"); len(got) != 0 {
		t.Fatalf("未知会话应为空表：%v", got)
	}

	// 启动一个约 2 秒的后台任务并带回显输出（断言日志投影）
	cmd := "echo bg-task-log && sleep 2"
	if runtime.GOOS == "windows" {
		cmd = "echo bg-task-log & ping -n 3 127.0.0.1"
	}
	args, err := json.Marshal(map[string]string{"action": "bg_start", "command": cmd})
	if err != nil {
		t.Fatal(err)
	}
	res, err := sh.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("bg_start 失败：%s", res.Content)
	}

	// 任务出现在快照：id / 命令 / 运行中
	snap := waitSnapshot(t, s, "s-bg", 5*time.Second, func(v BgTaskView) bool { return true })
	if snap.ID != "bg-1" || snap.Command != cmd || !snap.Running {
		t.Fatalf("运行中快照不符：got %+v want id=bg-1 command=%q running=true", snap, cmd)
	}

	// 结束后：运行态翻转、退出码 0、日志带回显
	done := waitSnapshot(t, s, "s-bg", 15*time.Second, func(v BgTaskView) bool { return !v.Running })
	if done.ExitCode != 0 {
		t.Fatalf("退出码应为 0：%+v", done)
	}
	if !strings.Contains(done.Log, "bg-task-log") {
		t.Fatalf("快照日志应含任务回显：%q", done.Log)
	}
}

// waitSnapshot 轮询快照直到谓词命中（后台任务收尾是异步的：起停各留宽限）。
func waitSnapshot(t *testing.T, s *ChatService, sessionID string, timeout time.Duration, pred func(BgTaskView) bool) BgTaskView {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := s.BgTasksSnapshot(sessionID)
		if len(got) == 1 && pred(got[0]) {
			return got[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("快照未在 %s 内满足条件：len=%d %+v", timeout, len(got), got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
