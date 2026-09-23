package shelltool

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"
)

// longCmd 返回一个持续约 10 秒且持续产出的命令（跨平台）。
// Windows 用 ping（每秒一行输出），其余用 shell 循环。
func longCmd() string {
	if runtime.GOOS == "windows" {
		return "ping -n 10 127.0.0.1"
	}
	return "for i in 1 2 3 4 5 6 7 8 9 10; do echo tick$i; sleep 1; done"
}

func args(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newTool(t *testing.T, opts Options) *Tool {
	t.Helper()
	opts.Root = t.TempDir()
	return New(opts)
}

// C-TOOL-1：超时可配且必在预算内返回（默认 120s，测试用短超时）。
func TestShellRun_TimeoutReturns(t *testing.T) {
	tool := newTool(t, Options{Timeout: 700 * time.Millisecond})

	start := time.Now()
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "run", "command": longCmd()}))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("mechanism error: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("timeout not enforced: took %v", elapsed)
	}
	if !res.TimedOut || !res.IsError {
		t.Fatalf("result = %+v, want TimedOut && IsError", res)
	}
}

// C-TOOL-2：超时必须返回已捕获的部分输出 + TIMEOUT 标记，不静默。
func TestShellRun_TimeoutPartialOutput(t *testing.T) {
	tool := newTool(t, Options{Timeout: 700 * time.Millisecond})

	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "run", "command": longCmd()}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("result = %+v, want TimedOut", res)
	}
	if !strings.Contains(res.Content, "TIMEOUT") {
		t.Fatalf("content must carry TIMEOUT marker: %q", res.Content)
	}
	if len(strings.TrimSpace(res.Content)) <= len("[TIMEOUT]") {
		t.Fatalf("partial output missing: %q", res.Content)
	}
}

// C-TOOL-3：非零退出码是业务失败（IsError + 可读原因），不是机制 error。
func TestShellRun_BusinessError(t *testing.T) {
	tool := newTool(t, Options{Timeout: 5 * time.Second})

	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "run", "command": "exit 7"}))
	if err != nil {
		t.Fatalf("non-zero exit must not be mechanism error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("result = %+v, want IsError", res)
	}
	if !strings.Contains(res.Content, "7") {
		t.Fatalf("content must expose exit code: %q", res.Content)
	}
}

// C-TOOL-5：ctx 取消 → 立即返回 TimedOut 终态（进程树终止）。
func TestShellRun_CancelKillsTree(t *testing.T) {
	tool := newTool(t, Options{Timeout: 30 * time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	res, err := tool.Execute(ctx, args(t, map[string]any{"action": "run", "command": longCmd()}))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("cancel not honored: took %v", time.Since(start))
	}
	if !res.TimedOut {
		t.Fatalf("result = %+v, want TimedOut after cancel", res)
	}
}

// burstLongCmd 先突发大量输出，再保持运行（验证"有界 + 仍在运行"两件事）。
func burstLongCmd() string {
	if runtime.GOOS == "windows" {
		// 注意括号分组：否则 && 会被解析进 for 体内（只输出一行就转 ping）
		return "(for /l %i in (1,1,500) do @echo padding-line-%i) && ping -n 30 127.0.0.1"
	}
	return "for i in $(seq 1 500); do echo padding-line-$i; done; sleep 30"
}

// C-TOOL-4：后台任务日志缓冲有界（超限截断并标注）。
func TestShellRun_BackgroundLogBounded(t *testing.T) {
	tool := newTool(t, Options{BGLogLimit: 256})

	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "bg_start", "command": burstLongCmd()}))
	if err != nil || res.IsError {
		t.Fatalf("bg_start failed: %v %s", err, res.Content)
	}
	var start struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(res.Content), &start); err != nil || start.TaskID == "" {
		t.Fatalf("bg_start content = %q, want JSON with task_id", res.Content)
	}
	// 无论断言是否失败都必须回收后台进程（否则进程泄漏 + TempDir 无法清理）
	defer func() {
		_, _ = tool.Execute(context.Background(), args(t, map[string]any{"action": "bg_kill", "task_id": start.TaskID}))
		time.Sleep(300 * time.Millisecond)
	}()

	time.Sleep(1200 * time.Millisecond) // 等突发输出写满上限

	res, err = tool.Execute(context.Background(), args(t, map[string]any{"action": "bg_status", "task_id": start.TaskID}))
	if err != nil || res.IsError {
		t.Fatalf("bg_status failed: %v %s", err, res.Content)
	}
	var st struct {
		Running bool   `json:"running"`
		Log     string `json:"log"`
	}
	if err := json.Unmarshal([]byte(res.Content), &st); err != nil {
		t.Fatalf("bg_status content = %q", res.Content)
	}
	if !st.Running {
		t.Fatalf("task should still be running: %s", res.Content)
	}
	if len(st.Log) > 256+64 { // 上限 + 截断标记余量
		t.Fatalf("log not bounded: %d bytes", len(st.Log))
	}
	if !strings.Contains(st.Log, "truncated") {
		t.Fatalf("truncation must be marked: %q", st.Log)
	}
}
