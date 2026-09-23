// Package gittool 实现只读 git 工具（status / diff / log）。
// 做什么：让模型查看仓库状态与变更，作为"跑测试→读输出→修文件"闭环的观察手段。
// 被谁依赖：internal/app（装配进工具注册表）。
// 依赖谁：core/tools 端口、stdlib（调用系统 git 可执行文件）。
//
// 为什么只读：add/commit/push 是不可逆的仓库状态变更，MVP 不纳入；
// 需要时按 ADR-0005 准入判据复评后再加（且届时必须走审批链，见 M5 之后的路线）。
package gittool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"tiancode/internal/core/tools"
)

// gitTimeout 是单次 git 命令超时（只读操作毫秒级，30s 防御病态仓库）。
const gitTimeout = 30 * time.Second

// outputLimit 是输出上限（git diff 可能极大）。
const outputLimit = 64 * 1024

// Tool 是只读 git 工具。
type Tool struct {
	root string
}

// New 构造工具，root 为仓库工作目录。
func New(root string) *Tool { return &Tool{root: root} }

// Name 实现工具端口。
func (t *Tool) Name() string { return "git" }

// Description 实现工具端口。
func (t *Tool) Description() string {
	return "只读查看 git 仓库：status（变更列表）/ diff（差异）/ log（最近提交）"
}

// Schema 实现工具端口。
func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["status", "diff", "log"]},
    "path": {"type": "string", "description": "可选：限定单个路径"},
    "limit": {"type": "integer", "description": "log 时可选，返回条数上限（默认 10）"}
  },
  "required": ["action"]
}`)
}

// Execute 实现工具端口（内部超时 + 输出有界 + 业务失败走 IsError）。
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tools.ToolResult, error) {
	var a struct {
		Action string `json:"action"`
		Path   string `json:"path"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return businessErrf("invalid arguments: %v", err), nil
	}

	gitArgs, err := buildArgs(a.Action, a.Path, a.Limit)
	if err != nil {
		return businessErrf("%v", err), nil
	}

	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", gitArgs...)
	cmd.Dir = t.root
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf

	if err := cmd.Run(); err != nil {
		out := truncate(buf.String())
		if runCtx.Err() == context.DeadlineExceeded {
			return tools.ToolResult{Content: out + "\n[TIMEOUT: git killed]", IsError: true, TimedOut: true}, nil
		}
		return businessErrf("git %s failed: %s\n%v", a.Action, strings.TrimSpace(out), err), nil
	}
	out := strings.TrimSpace(buf.String())
	if out == "" {
		out = "(empty)"
	}
	return tools.ToolResult{Content: truncate(out)}, nil
}

// buildArgs 将动作映射为只读 git 参数（白名单式，绝不拼接任意用户输入到 shell）。
func buildArgs(action, path string, limit int) ([]string, error) {
	switch action {
	case "status":
		args := []string{"status", "--porcelain"}
		if path != "" {
			args = append(args, "--", path)
		}
		return args, nil
	case "diff":
		args := []string{"diff"}
		if path != "" {
			args = append(args, "--", path)
		}
		return args, nil
	case "log":
		if limit <= 0 {
			limit = 10
		}
		return []string{"log", "--oneline", "-n", fmt.Sprint(limit)}, nil
	default:
		return nil, fmt.Errorf("unknown action %q (want status/diff/log)", action)
	}
}

// truncate 截断超长输出并标注。
func truncate(s string) string {
	if len(s) <= outputLimit {
		return s
	}
	return s[:outputLimit] + "\n... [truncated]"
}

func businessErrf(format string, a ...any) tools.ToolResult {
	return tools.ToolResult{Content: fmt.Sprintf(format, a...), IsError: true}
}
