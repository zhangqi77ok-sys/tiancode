package terminal

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTerminalTool_DaemonExecution_And_Lifecycle(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot get wd: %v", err)
	}

	tool := NewTool(wd)
	ctx := context.Background()

	// 1. 启动后台守护任务 (例如 ping 或 sleep)
	// 在 Windows 上使用 ping -n 4 127.0.0.1 模拟运行约 3 秒的后台服务
	runArgs, _ := json.Marshal(map[string]any{
		"action":    "run",
		"command":   "ping -n 4 127.0.0.1",
		"is_daemon": true,
	})

	startRes, err := tool.Execute(ctx, runArgs)
	if err != nil || startRes.IsError {
		t.Fatalf("failed to start daemon task: %v, content: %s", err, startRes.Content)
	}

	if !strings.Contains(startRes.Content, "[Daemon Started]") {
		t.Errorf("expected '[Daemon Started]' in output, got: %s", startRes.Content)
	}

	// 提取 Task ID
	lines := strings.Split(startRes.Content, "\n")
	var taskID string
	for _, l := range lines {
		if strings.HasPrefix(l, "Task ID: ") {
			taskID = strings.TrimPrefix(l, "Task ID: ")
			break
		}
	}
	if taskID == "" {
		t.Fatalf("failed to parse task ID from output: %s", startRes.Content)
	}

	time.Sleep(200 * time.Millisecond)

	// 2. 查询后台任务状态
	statusArgs, _ := json.Marshal(map[string]any{
		"action":  "status",
		"task_id": taskID,
	})
	statusRes, err := tool.Execute(ctx, statusArgs)
	if err != nil || statusRes.IsError {
		t.Fatalf("status query failed: %v, content: %s", err, statusRes.Content)
	}
	if !strings.Contains(statusRes.Content, "Status: RUNNING") && !strings.Contains(statusRes.Content, "Status: FINISHED") {
		t.Errorf("expected valid status, got: %s", statusRes.Content)
	}

	// 3. 强力终止后台守护任务
	killArgs, _ := json.Marshal(map[string]any{
		"action":  "kill",
		"task_id": taskID,
	})
	killRes, err := tool.Execute(ctx, killArgs)
	if err != nil || killRes.IsError {
		t.Fatalf("kill task failed: %v, content: %s", err, killRes.Content)
	}
	if !strings.Contains(killRes.Content, "terminated") {
		t.Errorf("expected terminated in kill output, got: %s", killRes.Content)
	}

	// 4. 再次检查状态确认为 STOPPED / TERMINATED
	statusRes2, _ := tool.Execute(ctx, statusArgs)
	if !strings.Contains(statusRes2.Content, "Status: TERMINATED") && !strings.Contains(statusRes2.Content, "Status: FINISHED") {
		t.Errorf("expected terminated or finished status after kill, got: %s", statusRes2.Content)
	}
}
