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
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

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

	// progress 是界面过程推送回调（tools.ProgressSink，0.0.07）：前台命令执行中
	// 每 ≤500ms 把已捕获输出推一次（agent 层随事件带 CallID，前端更新同一张卡）。
	// 单槽：同一会话的前台命令串行执行（agent 循环互斥），不需要多路复用。
	pmu      sync.Mutex
	progress func(string)
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

// SetProgress 实现 tools.ProgressSink：cb 为 nil 表示清除（执行结束/换步时）。
func (t *Tool) SetProgress(cb func(string)) {
	t.pmu.Lock()
	t.progress = cb
	t.pmu.Unlock()
}

// pushProgress 把过程快照推给界面（无回调或空输出时静默）。
func (t *Tool) pushProgress(partial string) {
	t.pmu.Lock()
	cb := t.progress
	t.pmu.Unlock()
	if cb != nil && strings.TrimSpace(partial) != "" {
		cb(partial)
	}
}

// Close 终止全部后台任务并释放资源（0.2.35 审计#2）。工作区切换会整体替换
// shell 工具实例，应用退出也要收——孤儿后台进程与"退出后残留 node.exe"同源。
func (t *Tool) Close() error {
	t.bg.Close()
	return nil
}

// Name 实现工具端口。
func (t *Tool) Name() string { return "shell" }

// Description 实现工具端口。
// 为什么写明 shell 类型：模型曾把 PowerShell 语法（Get-ChildItem）喂给 cmd.exe
// 导致 exit 255（0.2.4 实测）——工具描述必须显式告知 shell 类型，模型才能选对命令。
func (t *Tool) Description() string {
	return "执行命令（Windows 为 cmd.exe：用 dir/find/type 等内置命令，不要用 Get-ChildItem 等 PowerShell 语法；Unix 为 sh）。" +
		"run：前台，默认 120s 超时并返回部分输出；bg_start/bg_status/bg_kill：后台任务。" +
		"输出已按系统代码页自动解码为 UTF-8。"
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
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (res tools.ToolResult, err error) {
	var a struct {
		Action         string `json:"action"`
		Command        string `json:"command"`
		TaskID         string `json:"task_id"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return businessErrf("invalid arguments: %v", err), nil
	}
	// 超时硬顶（0.2.35 审计#6）：timeout_seconds 无上限会让一条命令挂死整个
	// 回合；600s 覆盖 npm install 等重任务，更长的让模型分步处理。
	if a.TimeoutSeconds > 600 {
		a.TimeoutSeconds = 600
	}
	// 卡片语义标签：run/bg_start 显示命令首段（参考稿的"⌨ 命令"形态），bg_* 显示任务号
	defer func() {
		if res.Title == "" {
			res.Op = "exec"
			switch {
			case a.Command != "":
				res.Title = tools.Headline(a.Command, 120)
			case a.TaskID != "":
				res.Title = "task " + a.TaskID
			}
		}
	}()
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

	// 过程推送（0.0.07）：每 500ms 把已捕获输出推给界面一次（变化才推）——
	// 长命令几分钟黑盒 → 卡片随过程增长。模型上下文不受影响：只在命令结束时
	// 收到一次最终结果（保持头加尾截断）。取消/超时后已推送的部分自然保留。
	stop := make(chan struct{})
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		last := ""
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s := strings.TrimRight(buf.String(), "\n")
				if s != "" && s != last {
					last = s
					t.pushProgress(s)
				}
			}
		}
	}()
	err := cmd.Run()
	close(stop)
	<-pumpDone

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
			Content:  fmt.Sprintf("%s\n[已中断：进程树已终止，以上为部分输出]", strings.TrimRight(out, "\n")),
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

// decodeConsoleOutput 把控制台输出解码为 UTF-8 文本。
// 中文 Windows 的 cmd 输出是 OEM 代码页（简中 = GBK/CP936），直接按 UTF-8 解释会得到
// 满屏替换符（0.2.4 实测）。策略对齐开源工具（VS Code terminal 等）：
// 合法 UTF-8 原样通过；否则按 GBK 解码；都解不开就原样返回（宁可乱码可见，不静默造数据）。
func decodeConsoleOutput(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	out, err := simplifiedchinese.GBK.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(out)
}
