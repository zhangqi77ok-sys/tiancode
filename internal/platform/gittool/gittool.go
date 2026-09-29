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
	"unicode/utf8"

	"tiancode/internal/core/tools"
)

// gitTimeout 是单次 git 命令超时（只读操作毫秒级，30s 防御病态仓库）。
const gitTimeout = 30 * time.Second

// outputLimit 是输出上限（git diff 可能极大）。
const outputLimit = 64 * 1024

// maxLogLimit 是 log 返回条数的硬顶（0.0.06）。
const maxLogLimit = 50

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
    "path": {"type": "string", "description": "可选：限定单个路径（status/diff/log 都支持）"},
    "limit": {"type": "integer", "description": "log 时可选，返回条数上限（默认 10，最大 50）"}
  },
  "required": ["action"]
}`)
}

// Execute 实现工具端口（内部超时 + 输出有界 + 业务失败走 IsError）。
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (res tools.ToolResult, err error) {
	var a struct {
		Action string `json:"action"`
		Path   string `json:"path"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return businessErrf("invalid arguments: %v", err), nil
	}
	// 卡片语义标签："git status" 式子命令作主标签（defer 覆盖全部返回路径）
	defer func() {
		if res.Title == "" {
			res.Op = "git"
			res.Title = "git " + a.Action
		}
	}()

	gitArgs, err := buildArgs(a.Action, a.Path, a.Limit)
	if err != nil {
		return businessErrf("%v", err), nil
	}

	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", gitArgs...)
	hideConsole(cmd)
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
		if limit > maxLogLimit {
			// 硬顶（0.0.06）：log --oneline 单行虽小，但上限封顶失控查询；
			// 需要更多历史的场景应引导模型缩小 path 范围
			limit = maxLogLimit
		}
		args := []string{"log", "--oneline", "-n", fmt.Sprint(limit)}
		// path 限定（0.0.06）：与 status/diff 同语义——pathspec 必须跟在 "--"
		// 之后，否则文件名恰与分支/选项同名时会被 git 误解
		if path != "" {
			args = append(args, "--", path)
		}
		return args, nil
	default:
		return nil, fmt.Errorf("unknown action %q (want status/diff/log)", action)
	}
}

// truncateToBytes 截断到 n 字节并回退到 UTF-8 边界（0.2.35 审计#8）：
// 按字节直接切会把中文切成非法 UTF-8，卡片与 diff 出现乱码。
func truncateToBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// truncate 截断超长输出（0.0.06 改头尾保留）：git diff/status/log 的关键信息
// 两头都有——diff 头部是文件清单、尾部是最新文件的变更块。只留头部会把
// 后面的文件变更整段截没，模型以为改动不存在。标注中间丢失字节数。
func truncate(s string) string {
	if len(s) <= outputLimit {
		return s
	}
	return tools.HeadTail(s, outputLimit)
}

func businessErrf(format string, a ...any) tools.ToolResult {
	return tools.ToolResult{Content: fmt.Sprintf(format, a...), IsError: true}
}
