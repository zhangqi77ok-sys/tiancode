package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAppDaemonManagement(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tiancode_daemon_mgmt_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	app := NewApp()
	if err := app.SetWorkspace(tempDir); err != nil {
		t.Fatalf("SetWorkspace failed: %v", err)
	}

	// 初始状态下守护任务应为空
	tasks, err := app.ListDaemonTasks()
	if err != nil {
		t.Fatalf("ListDaemonTasks failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 initial daemon tasks, got %d", len(tasks))
	}

	// 通过 terminal tool 启动一个后台常驻任务 (Windows 下 ping -t 127.0.0.1 或 powershell Start-Sleep)
	termTool, ok := app.registry.GetTool("tool.terminal")
	if !ok {
		t.Fatalf("tool.terminal not registered")
	}

	rawArgs, _ := json.Marshal(map[string]any{
		"action":    "run",
		"command":   "powershell -NoProfile -Command \"Start-Sleep -Seconds 100\"",
		"is_daemon": true,
	})
	res, err := termTool.Execute(context.Background(), rawArgs)
	if err != nil {
		t.Fatalf("exec_command daemon failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("daemon execution returned error: %s", res.Content)
	}

	// 稍作等待确保进程启动登记
	time.Sleep(100 * time.Millisecond)

	// 查询列表
	tasks, err = app.ListDaemonTasks()
	if err != nil {
		t.Fatalf("ListDaemonTasks after start failed: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatalf("expected at least 1 daemon task in list, got 0")
	}

	startedTask := tasks[0]
	if startedTask.Status != "running" {
		t.Errorf("task status expected running, got %s", startedTask.Status)
	}
	if !strings.Contains(startedTask.Command, "Start-Sleep") {
		t.Errorf("task command mismatch: %s", startedTask.Command)
	}

	// 终止任务
	if err := app.KillDaemonTask(startedTask.TaskID); err != nil {
		t.Fatalf("KillDaemonTask failed: %v", err)
	}

	// 再次查询，状态应为 killed
	time.Sleep(100 * time.Millisecond)
	tasks, _ = app.ListDaemonTasks()
	for _, task := range tasks {
		if task.TaskID == startedTask.TaskID {
			if task.Status != "killed" && task.Status != "finished" {
				t.Errorf("expected task to be killed, got status: %s", task.Status)
			}
		}
	}
}
