package shelltool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"tiancode/internal/core/tools"
)

// boundedBuffer 是有界输出缓冲：超过 limit 后丢弃新增内容并标注截断（C-TOOL-4）。
// 为什么必须有界：后台任务可长时间运行（dev server），无界缓冲会吃光内存
// （legacy terminal_tool.go:230 用裸 bytes.Buffer 的教训）。
type boundedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{limit: limit}
}

// Write 实现 io.Writer；超限后静默丢弃但标记截断（绝不因日志过多阻塞进程）。
func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.limit - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		b.buf.Write(p[:room])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	if b.truncated {
		s += "\n... [truncated]"
	}
	return s
}

// bgTask 是后台任务状态。
type bgTask struct {
	id      string
	command string
	pid     int
	started time.Time
	buf     *boundedBuffer
	cancel  context.CancelFunc

	mu       sync.Mutex
	done     bool
	exitCode int
	waitErr  error // 进程等待错误（非零退出时的 *exec.ExitError），供 bg_status 观测
}

// bgManager 管理后台任务集合。
type bgManager struct {
	mu       sync.Mutex
	tasks    map[string]*bgTask
	seq      int64
	logLimit int
}

func newBGManager(logLimit int) *bgManager {
	return &bgManager{tasks: make(map[string]*bgTask), logLimit: logLimit}
}

func (m *bgManager) get(id string) (*bgTask, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	return t, ok
}

// start 启动后台任务。为什么用独立 ctx：后台任务生命周期与单次工具调用解耦，
// 由 bg_kill 显式终止。
func (m *bgManager) start(command, root string) (tools.ToolResult, error) {
	if strings.TrimSpace(command) == "" {
		return businessErrf("command is required"), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := newCommand(ctx, command)
	cmd.Dir = root
	cmd.Cancel = killTreeFor(cmd)
	cmd.WaitDelay = 3 * time.Second
	buf := newBoundedBuffer(m.logLimit)
	cmd.Stdout, cmd.Stderr = buf, buf

	if err := cmd.Start(); err != nil {
		cancel()
		return businessErrf("background start failed: %v", err), nil
	}

	m.mu.Lock()
	m.seq++
	id := fmt.Sprintf("bg-%d", m.seq)
	task := &bgTask{
		id: id, command: command, pid: cmd.Process.Pid,
		started: time.Now(), buf: buf, cancel: cancel, exitCode: -1,
	}
	m.tasks[id] = task
	m.mu.Unlock()

	go func() {
		// 等待错误不丢弃：记入任务状态供 bg_status 观测（R2 守卫红线）
		err := cmd.Wait()
		task.mu.Lock()
		task.done = true
		task.waitErr = err
		if cmd.ProcessState != nil {
			task.exitCode = cmd.ProcessState.ExitCode()
		}
		task.mu.Unlock()
	}()

	content, _ := json.Marshal(map[string]any{
		"task_id": id, "pid": task.pid, "status": "running",
	})
	return tools.ToolResult{Content: string(content)}, nil
}

// status 返回任务状态与日志（已截断）。
func (m *bgManager) status(id string) (tools.ToolResult, error) {
	task, ok := m.get(id)
	if !ok {
		return businessErrf("unknown task_id %q", id), nil
	}
	task.mu.Lock()
	running, code, waitErr := !task.done, task.exitCode, task.waitErr
	task.mu.Unlock()
	payload := map[string]any{
		"task_id": id, "running": running, "exit_code": code, "log": task.buf.String(),
	}
	if waitErr != nil {
		payload["wait_error"] = waitErr.Error()
	}
	content, _ := json.Marshal(payload)
	return tools.ToolResult{Content: string(content)}, nil
}

// kill 终止任务（进程树）。
func (m *bgManager) kill(id string) (tools.ToolResult, error) {
	task, ok := m.get(id)
	if !ok {
		return businessErrf("unknown task_id %q", id), nil
	}
	task.cancel() // 触发 exec.Cmd.Cancel → 终止进程树
	content, _ := json.Marshal(map[string]any{"task_id": id, "status": "killed"})
	return tools.ToolResult{Content: string(content)}, nil
}

// asExitError 提取退出错误（std 封装，避免调用处裸 import errors）。
func asExitError(err error, target **exec.ExitError) bool {
	return errors.As(err, target)
}
