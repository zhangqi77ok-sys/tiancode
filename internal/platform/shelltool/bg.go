package shelltool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tiancode/internal/core/tools"
)

// boundedBuffer 是有界输出缓冲（0.0.06 改造）：头尾保留式——头部与尾部都在预算内，
// 超出部分丢弃并计数，String() 产出"头 + 中间丢失标注 + 尾"。
// 为什么必须有界：后台任务可长时间运行（dev server），无界缓冲会吃光内存
// （legacy terminal_tool.go:230 用裸 bytes.Buffer 的教训）。
// 为什么保尾：测试失败的 FAIL 汇总、命令的最终错误都在输出末尾——只留头部
// 会让模型对着开头的填充内容猜结局（0.0.06 用户要求）。
type boundedBuffer struct {
	mu sync.Mutex
	w  *tools.HeadTailWriter
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{w: tools.NewHeadTailWriter(limit)}
}

// Write 实现 io.Writer；永不阻塞、永不报错（绝不因日志过多卡住子进程）。
func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.w.Write(p)
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	// 控制台输出按系统代码页解码（中文 Windows 为 GBK）：先取原始字节段，
	// 逐段解码再拼接（截断点最多损失一个双字节字符，等同旧实现的解码时机）。
	head, tail, dropped := b.w.Parts()
	if dropped == 0 && len(tail) == 0 {
		return decodeConsoleOutput(head)
	}
	s := decodeConsoleOutput(head) +
		fmt.Sprintf("\n...[truncated: %d middle bytes omitted]...\n", dropped) +
		decodeConsoleOutput(tail)
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

// Close 终止全部后台任务（进程树）（0.2.35 审计#2）：管理器随 shell 工具实例
// 一起丢弃，应用退出或工作区切换时调用——否则任务表丢失，bg_status/bg_kill
// 再也够不着，进程成为孤儿。
func (m *bgManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		t.cancel() // 触发 exec.Cmd.Cancel → 终止进程树
	}
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

// BgTaskInfo 是后台任务的对外只读快照（界面「任务」面板经编排层拉取）。
type BgTaskInfo struct {
	ID        string    // 任务号（bg-N）
	Command   string    // 启动命令原文
	PID       int       // 进程号
	Running   bool      // 是否仍在运行
	ExitCode  int       // 退出码（运行中为启动时的 -1；被强杀时也是负值）
	StartedAt time.Time // 启动时刻
	Log       string    // 头尾保留的有界输出（已解码；与 bg_status 看到的同一份）
}

// snapshot 返回任务表快照（按任务号升序）。只读：不动任务表、不碰进程。
// log 在 task 锁外取：buf 自带锁，避免嵌套持锁。
func (m *bgManager) snapshot() []BgTaskInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.tasks))
	for id := range m.tasks {
		ids = append(ids, id)
	}
	// 任务号序：map 无序，快照顺序要稳定，界面列表才不跳动
	sort.Slice(ids, func(i, j int) bool { return bgSeq(ids[i]) < bgSeq(ids[j]) })
	out := make([]BgTaskInfo, 0, len(ids))
	for _, id := range ids {
		t := m.tasks[id]
		t.mu.Lock()
		info := BgTaskInfo{
			ID: t.id, Command: t.command, PID: t.pid,
			Running: !t.done, ExitCode: t.exitCode, StartedAt: t.started,
		}
		t.mu.Unlock()
		info.Log = t.buf.String()
		out = append(out, info)
	}
	return out
}

// bgSeq 提取 bg-N 的 N（解析失败排最后：防御性兜底，ID 由本包生成、正常恒可解析）。
func bgSeq(id string) int64 {
	const failSeq = int64(1) << 62
	n, err := strconv.ParseInt(strings.TrimPrefix(id, "bg-"), 10, 64)
	if err != nil {
		return failSeq
	}
	return n
}

// asExitError 提取退出错误（std 封装，避免调用处裸 import errors）。
func asExitError(err error, target **exec.ExitError) bool {
	return errors.As(err, target)
}
