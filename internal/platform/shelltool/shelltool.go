// Package shelltool 实现工作区受控的命令执行工具（前台 run + 后台任务）。
// 做什么：把模型的命令请求转换为受超时/取消/输出有界保护的进程执行。
// 被谁依赖：internal/app（装配进工具注册表）。
// 依赖谁：core/tools 端口、stdlib（本包无外部依赖）。
//
// 执行契约（C-TOOL-1~5）：
//   - C-TOOL-1 前台命令超时可配（默认 120s），必在预算内返回；
//   - C-TOOL-2 超时/中断返回已捕获部分输出 + TIMEOUT 标记，不静默；
//   - C-TOOL-3 非零退出码是业务失败（IsError），非机制 error；
//   - C-TOOL-4 后台任务日志缓冲有界（超限截断并标注）；
//   - C-TOOL-5 取消（ctx.Done）终止进程树（Windows taskkill /T /F）。
package shelltool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"tiancode/internal/core/tools"
)

// defaultTimeout 是前台命令默认超时。
// 为什么 120s：覆盖 npm install / go build 等常见慢命令；旧实现 60s 硬超时
// 导致这类命令必被强杀（legacy terminal_tool.go:272 的教训）。
const defaultTimeout = 120 * time.Second

// defaultBGLogLimit 是后台任务日志缓冲上限（64KB）。
const defaultBGLogLimit = 64 * 1024

// Options 是工具构造参数。
type Options struct {
	Root       string        // 工作目录（命令 cwd）
	Timeout    time.Duration // 前台超时；<=0 取 defaultTimeout
	BGLogLimit int           // 后台日志上限字节；<=0 取 defaultBGLogLimit
}

// Tool 是命令执行工具。
type Tool struct {
	root    string
	timeout time.Duration
	bg      *bgManager
}

// New 构造工具并补齐默认值。
func New(opts Options) *Tool {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.BGLogLimit <= 0 {
		opts.BGLogLimit = defaultBGLogLimit
	}
	return &Tool{
		root:    opts.Root,
		timeout: opts.Timeout,
		bg:      newBGManager(opts.BGLogLimit),
	}
}

// Name 实现工具端口。
func (t *Tool) Name() string { return "shell" }

// Description 实现工具端口。
func (t *Tool) Description() string {
	return "执行命令（run：前台，默认 120s 超时并返回部分输出；bg_start/bg_status/bg_kill：后台任务）"
}

// Schema 实现工具端口。
func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["run", "bg_start", "bg_status", "bg_kill"]},
    "command": {"type": "string", "description": "run / bg_start 时必填"},
    "task_id": {"type": "string", "description": "bg_status / bg_kill 时必填"},
    "timeout_seconds": {"type": "integer", "description": "run 时可选，覆盖默认超时"}
  },
  "required": ["action"]
}`)
}

// Execute 实现工具端口。
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tools.ToolResult, error) {
	var a struct {
		Action         string `json:"action"`
		Command        string `json:"command"`
		TaskID         string `json:"task_id"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return businessErrf("invalid arguments: %v", err), nil
	}
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{Content: "cancelled", IsError: true, TimedOut: true}, nil
	}
	switch a.Action {
	case "run":
		return t.run(ctx, a.Command, a.TimeoutSeconds)
	case "bg_start":
		return t.bg.start(a.Command, t.root)
	case "bg_status":
		return t.bg.status(a.TaskID)
	case "bg_kill":
		return t.bg.kill(a.TaskID)
	default:
		return businessErrf("unknown action %q (want run/bg_start/bg_status/bg_kill)", a.Action), nil
	}
}

// run 执行前台命令：超时可配、超时返回部分输出、非零退出码为业务失败。
func (t *Tool) run(ctx context.Context, command string, timeoutSeconds int) (tools.ToolResult, error) {
	if strings.TrimSpace(command) == "" {
		return businessErrf("command is required"), nil
	}
	timeout := t.timeout
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}
	// C-TOOL-1：超时预算在实现内部施加（arch_check R3 亦要求）
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := newCommand(runCtx, command)
	cmd.Dir = t.root
	buf := newBoundedBuffer(t.bg.logLimit) // 输出同样有界，防内存失控
	cmd.Stdout, cmd.Stderr = buf, buf
	// C-TOOL-5：取消时终止整棵进程树（Windows 下 cmd 会派生孙进程）
	cmd.Cancel = killTreeFor(cmd)
	// 进程被杀后给 I/O 泵最多 3s 收尾，避免 Wait 永久挂起
	cmd.WaitDelay = 3 * time.Second

	err := cmd.Run()

	out := buf.String()
	switch {
	case runCtx.Err() == context.DeadlineExceeded:
		// C-TOOL-2：超时返回部分输出 + 明确标记
		return tools.ToolResult{
			Content:  fmt.Sprintf("%s\n[TIMEOUT after %s: process tree killed; partial output above]", strings.TrimRight(out, "\n"), timeout),
			IsError:  true,
			TimedOut: true,
		}, nil
	case ctx.Err() != nil:
		return tools.ToolResult{
			Content:  fmt.Sprintf("%s\n[CANCELLED: process tree killed; partial output above]", strings.TrimRight(out, "\n")),
			IsError:  true,
			TimedOut: true,
		}, nil
	case err != nil:
		// C-TOOL-3：非零退出码是业务失败
		code := -1
		var exitErr *exec.ExitError
		if asExitError(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		return tools.ToolResult{
			Content: fmt.Sprintf("%s\n[exit code %d: %v]", strings.TrimRight(out, "\n"), code, err),
			IsError: true,
		}, nil
	}
	if strings.TrimSpace(out) == "" {
		out = "(no output)"
	}
	return tools.ToolResult{Content: out}, nil
}

// newCommand 构造平台适配的 shell 命令（Windows: cmd /c；其余: sh -c），
// 并施加平台进程属性（Windows 隐藏窗口 / Unix 独立进程组）。
func newCommand(ctx context.Context, command string) *exec.Cmd {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/d", "/s", "/c", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	setupProcAttr(cmd)
	return cmd
}

// killTreeFor 返回终止进程树的 Cancel 回调（exec.Cmd.Cancel 语义：无参、捕获 cmd）。
func killTreeFor(cmd *exec.Cmd) func() error {
	return func() error {
		if cmd.Process == nil || cmd.Process.Pid <= 0 {
			return nil
		}
		return killPIDTree(cmd.Process.Pid)
	}
}

// businessErrf 构造模型可见的业务失败。
func businessErrf(format string, a ...any) tools.ToolResult {
	return tools.ToolResult{Content: fmt.Sprintf(format, a...), IsError: true}
}
