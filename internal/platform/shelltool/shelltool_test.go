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

// 0.0.06：前台输出超限时必须"头 + 尾"保留——测试失败的 FAIL 汇总在末尾，
// 只留头部会让模型对着开头的填充行猜结局。缓冲预算用 BGLogLimit 注入
// （前台 run 与后台日志共用同一个缓冲上限）。
func TestShellRun_PreservesTailOnHugeOutput(t *testing.T) {
	tool := newTool(t, Options{BGLogLimit: 1024})
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "(for /l %i in (1,1,200) do @echo padding-line-%i) & echo FAIL: boom-at-the-end"
	} else {
		cmd = "for i in $(seq 1 200); do echo padding-line-$i; done; echo FAIL: boom-at-the-end"
	}
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "run", "command": cmd}))
	if err != nil || res.IsError {
		t.Fatalf("run failed: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "middle bytes omitted") {
		t.Fatalf("输出应超限并标注：%d bytes", len(res.Content))
	}
	if !strings.Contains(res.Content, "FAIL: boom-at-the-end") {
		t.Fatalf("尾部 FAIL 汇总被截没：%s", res.Content[len(res.Content)-160:])
	}
	if !strings.Contains(res.Content, "padding-line-1\r\n") && !strings.Contains(res.Content, "padding-line-1\n") {
		t.Fatalf("头部丢失：%s", res.Content[:80])
	}
}

func newTool(t *testing.T, opts Options) *Tool {
	t.Helper()
	opts.Root = t.TempDir()
	return New(opts)
}

// C-TOOL-1：超时可配且必在预算内返回（默认 120s，测试用短超时）。
// 墙钟断言用相对语义边界而非固定 3s：命令自然时长约 10s，若返回时间逼近它说明超时未生效；
// 收束来自超时预算（而非命令自然结束）已由 res.TimedOut 证明。固定 3s 在并发负载下会假红
// （docs/PENDING.md「已知脆弱点」1），故取 8s——远小于命令时长，又给进程树终止留足负载余量。
func TestShellRun_TimeoutReturns(t *testing.T) {
	tool := newTool(t, Options{Timeout: 700 * time.Millisecond})

	start := time.Now()
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "run", "command": longCmd()}))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("mechanism error: %v", err)
	}
	if elapsed > 8*time.Second {
		t.Fatalf("timeout not enforced: took %v (command itself runs ~10s)", elapsed)
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
// 同 TestShellRun_TimeoutReturns：相对语义边界（命令约 10s）代替固定 3s 墙钟，负载下不再假红。
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
	if time.Since(start) > 8*time.Second {
		t.Fatalf("cancel not honored: took %v (command itself runs ~10s)", time.Since(start))
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
// 等待用轮询（日志出现 truncated 即满足，总超时兜底）代替固定 sleep，
// 不依赖机器快慢——docs/TESTING.md 时序判据；超时仍无 truncated 则由末尾断言显式失败。
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

	var st struct {
		Running bool   `json:"running"`
		Log     string `json:"log"`
	}
	for deadline := time.Now().Add(10 * time.Second); ; {
		time.Sleep(200 * time.Millisecond)
		res, err = tool.Execute(context.Background(), args(t, map[string]any{"action": "bg_status", "task_id": start.TaskID}))
		if err != nil || res.IsError {
			t.Fatalf("bg_status failed: %v %s", err, res.Content)
		}
		if err := json.Unmarshal([]byte(res.Content), &st); err != nil {
			t.Fatalf("bg_status content = %q", res.Content)
		}
		// 满足条件或超时兜底都退出循环；未截断的失败交给末尾断言显式报告
		if strings.Contains(st.Log, "truncated") || time.Now().After(deadline) {
			break
		}
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

// 控制台输出解码：中文 Windows 的 cmd 输出为 OEM 代码页（简中 = GBK/CP936），
// 直接按 UTF-8 解释会得到满屏替换符（0.2.4 实测：find/dir 输出全是乱码，模型无法工作）。
// 策略对齐开源工具（VS Code terminal / open-interpreter 等）：
// 合法 UTF-8 原样通过；否则按 GBK 解码。测试用固定 GBK 字节，不依赖机器代码页。
func TestDecodeConsoleOutput_GBKFallback(t *testing.T) {
	// "中文" 的 GBK 字节：D6 D0 CE C4
	if got := decodeConsoleOutput([]byte{0xD6, 0xD0, 0xCE, 0xC4}); got != "中文" {
		t.Fatalf("GBK decode = %q, want 中文", got)
	}
	// 合法 UTF-8 原样通过（PowerShell/现代工具输出已是 UTF-8，不得二次转码）
	if got := decodeConsoleOutput([]byte("héllo 世界")); got != "héllo 世界" {
		t.Fatalf("UTF-8 passthrough = %q", got)
	}
	if got := decodeConsoleOutput(nil); got != "" {
		t.Fatalf("empty = %q, want empty", got)
	}
	if got := decodeConsoleOutput([]byte("plain ascii")); got != "plain ascii" {
		t.Fatalf("ascii = %q", got)
	}
}

func TestShellRun_ClampedTimeoutIsVisible(t *testing.T) {
	tool := newTool(t, Options{})
	res, err := tool.Execute(context.Background(), args(t, map[string]any{
		"action": "run", "command": "echo hi", "timeout_seconds": 9999,
	}))
	if err != nil || res.IsError {
		t.Fatalf("run failed: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "请求超时 9999 秒，已钳为 600") {
		t.Fatalf("钳制说明缺失：%s", res.Content)
	}
}
