// Package fstool 实现工作区受控文件工具（read/write/replace）。
// 做什么：把模型的结构化文件操作转换为受路径校验与原子写保护的磁盘操作。
// 被谁依赖：internal/app（装配进工具注册表）。
// 依赖谁：core/tools 端口、core/llm（定义形态）、platform/atomicfile、stdlib。
//
// 执行契约（C-FS-1~4、C-TOOL-1）：路径越界拒绝；replace 多处/零匹配报错且文件
// 零修改；write 走原子写；内部施加超时（30s——fs 操作快，宽裕即安全）。
package fstool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tiancode/internal/core/tools"
	"tiancode/internal/platform/atomicfile"
)

// fsTimeout 是单个文件操作的超时上限。
// 为什么 30s：本地磁盘操作毫秒级完成，30s 只防御文件系统病态（网络盘/杀软锁死）。
const fsTimeout = 30 * time.Second

// Tool 是工作区受控文件工具。
type Tool struct {
	root string // 工作区绝对路径，所有路径必须落在其内
}

// New 构造工具，root 为工作区绝对路径。
func New(root string) *Tool { return &Tool{root: root} }

// Root 返回工作区根路径（测试与审计用）。
func (t *Tool) Root() string { return t.root }

// Name 实现工具端口。
func (t *Tool) Name() string { return "fs" }

// Description 实现工具端口。
func (t *Tool) Description() string {
	return "读写工作区文件（read/write）与精准局部替换（replace，多处匹配默认拒绝）"
}

// Schema 实现工具端口：参数 JSON Schema。
func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["read", "write", "replace"]},
    "path": {"type": "string", "description": "相对工作区的路径"},
    "content": {"type": "string", "description": "write 时的完整文件内容"},
    "target": {"type": "string", "description": "replace 时的精确目标文本"},
    "replacement": {"type": "string", "description": "replace 时的替换文本"},
    "allow_multiple": {"type": "boolean", "description": "replace 多处匹配时是否全部替换（默认 false）"}
  },
  "required": ["action", "path"]
}`)
}

// Execute 实现工具端口（遵守执行契约：内部超时/业务失败走 IsError）。
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tools.ToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, fsTimeout)
	defer cancel()

	var args struct {
		Action        string `json:"action"`
		Path          string `json:"path"`
		Content       string `json:"content"`
		Target        string `json:"target"`
		Replacement   string `json:"replacement"`
		AllowMultiple bool   `json:"allow_multiple"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return bizErrf("invalid arguments: %v", err), nil
	}

	// 取消响应：入口即检查（C-TOOL-5 协作式取消）
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{Content: "cancelled", IsError: true, TimedOut: true}, nil
	}

	switch args.Action {
	case "read":
		return t.read(args.Path)
	case "write":
		return t.write(ctx, args.Path, args.Content)
	case "replace":
		return t.replace(ctx, args.Path, args.Target, args.Replacement, args.AllowMultiple)
	default:
		return bizErrf("unknown action %q (want read/write/replace)", args.Action), nil
	}
}

// resolve 校验并解析工作区内路径（C-FS-4：绝对路径与 ../ 逃逸一律拒绝）。
func (t *Tool) resolve(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path not allowed: %s", path)
	}
	clean := filepath.Clean(filepath.Join(t.root, path))
	if clean != t.root && !strings.HasPrefix(clean, t.root+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}
	return clean, nil
}

func (t *Tool) read(path string) (tools.ToolResult, error) {
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return bizErrf("read failed: %v", err), nil
	}
	return tools.ToolResult{Content: string(data)}, nil
}

func (t *Tool) write(ctx context.Context, path, content string) (tools.ToolResult, error) {
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	if err := atomicfile.WriteFileAtomic(full, []byte(content), 0o600); err != nil {
		return bizErrf("write failed: %v", err), nil
	}
	return tools.ToolResult{Content: fmt.Sprintf("written %s (%d bytes)", path, len(content))}, nil
}

func (t *Tool) replace(ctx context.Context, path, target, replacement string, allowMultiple bool) (tools.ToolResult, error) {
	if target == "" {
		return bizErrf("target is required for replace"), nil
	}
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return bizErrf("read before replace failed: %v", err), nil
	}
	count := strings.Count(string(data), target)
	if count == 0 {
		// C-FS-3：零匹配报错，文件零修改
		return bizErrf("target not found (0 matches): file unchanged"), nil
	}
	if count > 1 && !allowMultiple {
		// C-FS-2：多处匹配默认拒绝，防误伤
		return bizErrf("target matches %d locations; refusing ambiguous replace (set allow_multiple to replace all)", count), nil
	}
	updated := strings.ReplaceAll(string(data), target, replacement)
	if err := atomicfile.WriteFileAtomic(full, []byte(updated), 0o600); err != nil {
		return bizErrf("replace write failed: %v", err), nil
	}
	return tools.ToolResult{Content: fmt.Sprintf("replaced %d occurrence(s) in %s", count, path)}, nil
}

// bizErrf 构造模型可见的业务失败（ToolResult.IsError，而非机制 error）。
func bizErrf(format string, a ...any) tools.ToolResult {
	return tools.ToolResult{Content: fmt.Sprintf(format, a...), IsError: true}
}

func bizErr(err error) tools.ToolResult {
	return tools.ToolResult{Content: err.Error(), IsError: true}
}
