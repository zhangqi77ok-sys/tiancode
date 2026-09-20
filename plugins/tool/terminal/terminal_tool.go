package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	v1 "tiancode/pkg/plugin/v1"
)

// Tool 受控终端执行算子插件
// DaemonTask 后台常驻守护进程与长耗时任务状态
type DaemonTask struct {
	ID        string
	Command   string
	PID       int
	StartTime time.Time
	Cmd       *exec.Cmd
	Cancel    context.CancelFunc
	LogBuf    *bytes.Buffer
	Mu        sync.RWMutex
	Done      bool
	ExitCode  int
	Killed    bool
	Err       error
}

type Tool struct {
	id            string
	name          string
	version       string
	workspaceRoot string
	daemons       sync.Map
}

// NewTool 构造受控终端算子
func NewTool(root string) *Tool {
	return &Tool{
		id:            "tool.terminal",
		name:          "Controlled Terminal Tool",
		version:       "1.0.0",
		workspaceRoot: root,
	}
}

func (t *Tool) ID() string             { return t.id }
func (t *Tool) Name() string           { return t.name }
func (t *Tool) Version() string        { return t.version }
func (t *Tool) Type() v1.PluginType    { return v1.TypeTool }
func (t *Tool) Init(ctx context.Context, cfg json.RawMessage) error { return nil }
func (t *Tool) Start(ctx context.Context) error { return nil }
func (t *Tool) Stop(ctx context.Context) error  { return nil }
func (t *Tool) Health(ctx context.Context) v1.HealthStatus {
	return v1.HealthStatus{Healthy: true, Message: "Terminal tool ready"}
}

// Definition 声明给大模型的算子元数据契约
func (t *Tool) Definition() v1.ToolDefinition {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"run", "status", "kill"},
				"description": "终端动作: run (执行命令，默认), status (查询后台守护任务状态), kill (终止后台守护任务)",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "要执行的 Shell/CMD 命令，例如 'git status'、'npm test' 或 'dir' (action=run 时必填)",
			},
			"is_daemon": map[string]any{
				"type":        "boolean",
				"description": "是否以异步后台守护进程方式运行 (适用于长时间运行的服务，如 npm run dev、go run 等，立即返回 task_id)",
			},
			"background": map[string]any{
				"type":        "boolean",
				"description": "is_daemon 的别名",
			},
			"task_id": map[string]any{
				"type":        "string",
				"description": "目标后台任务标识符 (action=status 或 action=kill 时必填)",
			},
		},
	}
	schemaBytes, _ := json.Marshal(schema)

	return v1.ToolDefinition{
		Name:        "exec_command",
		Description: "在沙箱工作区根目录下受控静默执行命令行脚本，支持同步等待执行与后台常驻守护任务 (is_daemon: true)",
		Mutating:    true,
		Parameters:  schemaBytes,
	}
}

// Execute 物理执行命令 (严格注入 CREATE_NO_WINDOW 杜绝弹窗)
func (t *Tool) Execute(ctx context.Context, rawArgs json.RawMessage) (*v1.ToolResult, error) {
	var args struct {
		Action     string `json:"action"`
		Command    string `json:"command"`
		IsDaemon   bool   `json:"is_daemon"`
		Background bool   `json:"background"`
		TaskID     string `json:"task_id"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return &v1.ToolResult{Content: fmt.Sprintf("invalid args: %v", err), IsError: true}, nil
	}

	act := strings.ToLower(strings.TrimSpace(args.Action))
	if act == "" {
		act = "run"
	}

	switch act {
	case "kill":
		taskID := strings.TrimSpace(args.TaskID)
		if taskID == "" {
			return &v1.ToolResult{Content: "kill error: task_id is required", IsError: true}, nil
		}
		val, ok := t.daemons.Load(taskID)
		if !ok {
			return &v1.ToolResult{Content: fmt.Sprintf("kill error: task [%s] not found", taskID), IsError: true}, nil
		}
		task := val.(*DaemonTask)
		task.Mu.Lock()
		task.Killed = true
		if task.Cancel != nil {
			task.Cancel()
		}
		if task.Cmd != nil && task.Cmd.Process != nil && task.Cmd.Process.Pid > 0 {
			if runtime.GOOS == "windows" {
				killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", task.Cmd.Process.Pid))
				killCmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
				_ = killCmd.Run()
			} else {
				_ = task.Cmd.Process.Kill()
			}
		}
		task.Done = true
		task.Mu.Unlock()
		return &v1.ToolResult{Content: fmt.Sprintf("daemon task [%s] (PID %d) terminated successfully", taskID, task.PID), IsError: false}, nil

	case "status":
		taskID := strings.TrimSpace(args.TaskID)
		if taskID == "" {
			return &v1.ToolResult{Content: "status error: task_id is required", IsError: true}, nil
		}
		val, ok := t.daemons.Load(taskID)
		if !ok {
			return &v1.ToolResult{Content: fmt.Sprintf("status error: task [%s] not found", taskID), IsError: true}, nil
		}
		task := val.(*DaemonTask)
		task.Mu.RLock()
		status := "RUNNING"
		if task.Killed {
			status = "TERMINATED"
		} else if task.Done {
			status = "FINISHED"
		}
		elapsed := time.Since(task.StartTime).Round(time.Millisecond)
		logs := task.LogBuf.String()
		if len(logs) > 4096 {
			logs = "...[truncated]...\n" + logs[len(logs)-4096:]
		}
		task.Mu.RUnlock()
		return &v1.ToolResult{
			Content: fmt.Sprintf("Task ID: %s\nStatus: %s\nPID: %d\nCommand: %s\nElapsed: %v\nRecent Logs:\n%s", task.ID, status, task.PID, task.Command, elapsed, logs),
			IsError: false,
		}, nil

	case "run":
		cmdStr := strings.TrimSpace(args.Command)
		if cmdStr == "" {
			return &v1.ToolResult{Content: "command is required", IsError: true}, nil
		}

		// 后台常驻守护任务支持
		if args.IsDaemon || args.Background {
			taskID := fmt.Sprintf("task-%d", time.Now().UnixNano()%10000000)
			bgCtx, bgCancel := context.WithCancel(context.Background())
			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.CommandContext(bgCtx, "cmd", "/d", "/s", "/c", cmdStr)
				cmd.SysProcAttr = &syscall.SysProcAttr{
					CreationFlags: 0x08000000,
					HideWindow:    true,
				}
			} else {
				cmd = exec.CommandContext(bgCtx, "sh", "-c", cmdStr)
			}
			cmd.Dir = t.workspaceRoot
			logBuf := new(bytes.Buffer)
			cmd.Stdout = logBuf
			cmd.Stderr = logBuf

			if err := cmd.Start(); err != nil {
				bgCancel()
				return &v1.ToolResult{Content: fmt.Sprintf("daemon start error: %v", err), IsError: true}, nil
			}

			pid := 0
			if cmd.Process != nil {
				pid = cmd.Process.Pid
			}

			task := &DaemonTask{
				ID:        taskID,
				Command:   cmdStr,
				PID:       pid,
				StartTime: time.Now(),
				Cmd:       cmd,
				Cancel:    bgCancel,
				LogBuf:    logBuf,
			}
			t.daemons.Store(taskID, task)

			go func() {
				err := cmd.Wait()
				task.Mu.Lock()
				task.Done = true
				if cmd.ProcessState != nil {
					task.ExitCode = cmd.ProcessState.ExitCode()
				}
				task.Err = err
				task.Mu.Unlock()
			}()

			return &v1.ToolResult{
				Content: fmt.Sprintf("[Daemon Started]\nTask ID: %s\nPID: %d\nCommand: %s\nStatus: RUNNING\nDescription: Background process is running. Use action='status' to query or action='kill' to terminate.", taskID, pid, cmdStr),
				IsError: false,
			}, nil
		}

		execCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(execCtx, "cmd", "/d", "/s", "/c", cmdStr)
		// 关键铁律: 注入 CREATE_NO_WINDOW = 0x08000000 杜绝 Windows 黑色控制台弹窗
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: 0x08000000,
			HideWindow:    true,
		}
		cmd.Cancel = func() error {
			if cmd.Process != nil && cmd.Process.Pid > 0 {
				killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
				killCmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
				return killCmd.Run()
			}
			return nil
		}
	} else {
		cmd = exec.CommandContext(execCtx, "sh", "-c", args.Command)
		cmd.Cancel = func() error {
			if cmd.Process != nil && cmd.Process.Pid > 0 {
				return cmd.Process.Kill()
			}
			return nil
		}
	}

	cmd.Dir = t.workspaceRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return &v1.ToolResult{
			Content: fmt.Sprintf("cmd start error: %v", err),
			IsError: true,
		}, nil
	}

	// 注入 context 取消守护：彻底杀死整棵孤儿子进程树，防止后台挂死
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-execCtx.Done():
			if cmd.Process != nil {
				if runtime.GOOS == "windows" {
					killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
					killCmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
					_ = killCmd.Run()
				} else {
					_ = cmd.Process.Kill()
				}
			}
		}
	}()

	err := cmd.Wait()
	close(done)
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n[stderr]:\n" + stderr.String()
		} else {
			output = stderr.String()
		}
	}

	if err != nil {
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		return &v1.ToolResult{
			Content: fmt.Sprintf("Exit Code: %d\nOutput:\n%s\nError: %v", exitCode, output, err),
			IsError: true,
		}, nil
	}

		return &v1.ToolResult{
			Content: output,
			IsError: false,
		}, nil

	default:
		return &v1.ToolResult{Content: fmt.Sprintf("unknown action: %s", act), IsError: true}, nil
	}
}

// StreamChunkHandler 流式输出回调
type StreamChunkHandler func(chunk string)

// ExecuteStream 实时流式执行终端指令，边读边回调 onChunk，并在命令结束时返回 exitCode
func (t *Tool) ExecuteStream(ctx context.Context, command string, onChunk StreamChunkHandler) (int, error) {
	cmdStr := strings.TrimSpace(command)
	if cmdStr == "" {
		return -1, fmt.Errorf("command is required")
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/d", "/s", "/c", cmdStr)
		// 关键铁律: 注入 CREATE_NO_WINDOW = 0x08000000 杜绝 Windows 黑色控制台弹窗
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: 0x08000000,
			HideWindow:    true,
		}
		cmd.Cancel = func() error {
			if cmd.Process != nil && cmd.Process.Pid > 0 {
				killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
				killCmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
				return killCmd.Run()
			}
			return nil
		}
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Cancel = func() error {
			if cmd.Process != nil && cmd.Process.Pid > 0 {
				return cmd.Process.Kill()
			}
			return nil
		}
	}

	cmd.Dir = t.workspaceRoot

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return -1, fmt.Errorf("stdout pipe error: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return -1, fmt.Errorf("stderr pipe error: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("cmd start error: %w", err)
	}

	// 注入 context 取消守护：防止进程树孤儿与管道锁死
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-ctx.Done():
			if cmd.Process != nil {
				if runtime.GOOS == "windows" {
					killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
					killCmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
					_ = killCmd.Run()
				} else {
					_ = cmd.Process.Kill()
				}
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)

	readerFunc := func(r io.Reader) {
		defer wg.Done()
		buf := make([]byte, 1024)
		for {
			n, err := r.Read(buf)
			if n > 0 && onChunk != nil {
				onChunk(string(buf[:n]))
			}
			if err != nil {
				break
			}
		}
	}

	go readerFunc(stdoutPipe)
	go readerFunc(stderrPipe)

	wg.Wait()
	err = cmd.Wait()
	close(done)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return exitCode, err
}
