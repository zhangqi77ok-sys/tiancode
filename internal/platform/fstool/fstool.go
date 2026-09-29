// Package fstool 实现工作区受控文件工具（read/write/replace/list）。
// 做什么：把模型的结构化文件操作转换为受路径校验与原子写保护的磁盘操作。
// 被谁依赖：internal/app（装配进工具注册表）。
// 依赖谁：core/tools 端口、core/llm（定义形态）、platform/atomicfile、stdlib。
//
// 执行契约（C-FS-1~7、C-TOOL-1）：路径越界拒绝；replace 多处/零匹配报错且文件
// 零修改；write 走原子写；list 非递归且有界；内部施加超时（30s——fs 操作快，宽裕即安全）。
package fstool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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
	return "读写工作区文件（read/write）、精准局部替换（replace，多处匹配默认拒绝）与非递归目录列表（list，最多 500 条）"
}

// Schema 实现工具端口：参数 JSON Schema。
func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["read", "write", "replace", "list"]},
    "path": {"type": "string", "description": "相对工作区的路径"},
    "content": {"type": "string", "description": "write 时的完整文件内容"},
    "target": {"type": "string", "description": "replace 时的精确目标文本"},
    "replacement": {"type": "string", "description": "replace 时的替换文本"},
    "allow_multiple": {"type": "boolean", "description": "replace 多处匹配时是否全部替换（默认 false）"},
    "offset": {"type": "integer", "description": "read 时的起始字节偏移（大文件分段读取）"},
    "length": {"type": "integer", "description": "read 时的读取字节数（默认到文件尾，单次上限 10MB）"}
  },
  "required": ["action", "path"]
}`)
}

// Execute 实现工具端口（遵守执行契约：内部超时/业务失败走 IsError）。
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (res tools.ToolResult, err error) {
	ctx, cancel := context.WithTimeout(ctx, fsTimeout)
	defer cancel()

	var args struct {
		Action        string `json:"action"`
		Path          string `json:"path"`
		Content       string `json:"content"`
		Target        string `json:"target"`
		Replacement   string `json:"replacement"`
		AllowMultiple bool   `json:"allow_multiple"`
		Offset        int64  `json:"offset"`
		Length        int64  `json:"length"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return bizErrf("invalid arguments: %v", err), nil
	}

	// 卡片语义标签：defer 覆盖全部返回路径（成功/业务失败/取消），失败卡也能显示"动了哪个文件"
	defer func() {
		if res.Title == "" {
			res.Title, res.Op = fsTitle(args.Path), fsOp(args.Action)
		}
	}()

	// 取消响应：入口即检查（C-TOOL-5 协作式取消）
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{Content: "cancelled", IsError: true, TimedOut: true}, nil
	}

	// 写侧参数上限（0.2.36 审计 R6）：上限高于读取（可编辑锁文件/生成产物），
	// 超限**整次失败**——不截断、不落盘（原子写保证失败不留下半个文件）。
	// 已知局限：这是文件工具层的第一道闸（JSON 反序列化前内存已分配）；
	// 真正挡住超大工具参数还需要 agent 参数层的长度限制（另一层，另行处理）。
	if len(args.Content) > maxWriteBytes {
		return bizErrf("content too large (%d bytes > %d)：请拆分为多次写入", len(args.Content), maxWriteBytes), nil
	}
	if len(args.Replacement) > maxWriteBytes || len(args.Target) > maxWriteBytes {
		return bizErrf("target/replacement too large (> %d)：请拆分替换", maxWriteBytes), nil
	}

	switch args.Action {
	case "read":
		return t.read(args.Path, args.Offset, args.Length)
	case "write":
		return t.write(ctx, args.Path, args.Content)
	case "replace":
		return t.replace(ctx, args.Path, args.Target, args.Replacement, args.AllowMultiple)
	case "list":
		return t.list(args.Path)
	default:
		return bizErrf("unknown action %q (want read/write/replace/list)", args.Action), nil
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
	// 符号链接/junction 解析（0.2.35 审计#4）：工作区内的链接可以指到区外，
	// 词法前缀检查拦不住——对已存在的最深前缀解析真实路径后再比对。
	// 目标不存在（写场景）时对父目录解析（父目录在区内，链接才能落到区外）。
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		clean = real
	} else if real, err := filepath.EvalSymlinks(filepath.Dir(clean)); err == nil {
		clean = filepath.Join(real, filepath.Base(clean))
	}
	if !hasRootPrefix(t.root, clean) {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}
	return clean, nil
}

// hasRootPrefix 判断 p 是否在 root 内（0.2.36 审计 R4/R5：两侧同一套规范化；
// Windows 文件系统大小写不敏感——前缀比较必须忽略大小写，否则 C:\Proj 与
// c:\proj 会把整个工作区判成越界）。
func hasRootPrefix(root, p string) bool {
	if p == root {
		return true
	}
	if runtime.GOOS == "windows" {
		p, root = strings.ToLower(p), strings.ToLower(root)
	}
	return strings.HasPrefix(p, root+string(filepath.Separator))
}

// 读取与写入的硬顶（0.2.36 审计 R3/R6）：
//   - maxReadBytes：单次 read（整读或单个分片）上限；
//   - maxWriteBytes：write/replace 的 content/target/replacement 上限——**高于读取**
//     （锁文件/打包产物等大文本仍可编辑）；超限整次失败、不截断、原文件不动。
const (
	maxReadBytes  = 10 << 20
	maxWriteBytes = 32 << 20
)

// read 读取文件（支持 offset/length 分段）。超限语义（0.2.36 审计 R3）：
// 整读超过硬顶时**拒绝**并给出分段读取的合法出路——不把模型指去 shell
// 绕开上限（读取上限必须对模型是可满足的约束，而不是可规避的建议）。
// 分片按 UTF-8 边界回退（与摘要/日志截断同一纪律，绝不切出非法 UTF-8）。
func (t *Tool) read(path string, offset, length int64) (tools.ToolResult, error) {
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	info, err := os.Stat(full)
	if err != nil {
		return bizErrf("read failed: %v", err), nil
	}
	size := info.Size()

	if offset == 0 && length == 0 {
		// 整读路径
		if size > maxReadBytes {
			return bizErrf("file too large (%d bytes > %d)：请用 offset/length 分段读取（如 {\"action\":\"read\",\"path\":%q,\"offset\":0,\"length\":%d}）",
				size, maxReadBytes, path, maxReadBytes), nil
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return bizErrf("read failed: %v", err), nil
		}
		return tools.ToolResult{Content: string(data)}, nil
	}

	// 分段读取路径
	if offset < 0 || length < 0 {
		return bizErrf("offset/length 必须非负"), nil
	}
	if length == 0 || length > maxReadBytes {
		length = maxReadBytes
	}
	if offset >= size {
		return bizErrf("offset %d 超出文件大小 %d", offset, size), nil
	}
	f, err := os.Open(full)
	if err != nil {
		return bizErrf("read failed: %v", err), nil
	}
	defer f.Close()
	buf := make([]byte, length)
	n, readErr := f.ReadAt(buf, offset)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return bizErrf("read failed: %v", readErr), nil
	}
	chunk := buf[:n]
	for len(chunk) > 0 && !utf8.Valid(chunk) {
		chunk = chunk[:len(chunk)-1]
	}
	return tools.ToolResult{Content: fmt.Sprintf("[offset=%d 读取 %d 字节 / 共 %d 字节]\n%s", offset, len(chunk), size, string(chunk))}, nil
}

func (t *Tool) write(ctx context.Context, path, content string) (tools.ToolResult, error) {
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	// 读旧内容只为生成 diff；文件不存在＝新建（正常），其他读失败则显式报错不静默
	var old []byte
	if data, readErr := os.ReadFile(full); readErr == nil {
		old = data
	} else if !os.IsNotExist(readErr) {
		return bizErrf("read before write failed: %v", readErr), nil
	}
	if err := atomicfile.WriteFileAtomic(full, []byte(content), 0o600); err != nil {
		return bizErrf("write failed: %v", err), nil
	}
	return tools.ToolResult{
		Content: fmt.Sprintf("written %s (%d bytes)", path, len(content)),
		Diff:    diffText(path, string(old), content),
	}, nil
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
	return tools.ToolResult{
		Content: fmt.Sprintf("replaced %d occurrence(s) in %s", count, path),
		Diff:    diffText(path, string(data), updated),
	}, nil
}

const listLimit = 500

func (t *Tool) list(path string) (tools.ToolResult, error) {
	if path == "" {
		path = "."
	}
	full, err := t.resolve(path)
	if err != nil {
		return bizErr(err), nil
	}
	info, err := os.Stat(full)
	if err != nil {
		return bizErrf("list failed: %v", err), nil
	}
	if !info.IsDir() {
		return bizErrf("not a directory: %s", path), nil
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return bizErrf("list failed: %v", err), nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})
	total := len(entries)
	if total == 0 {
		return tools.ToolResult{Content: "empty directory"}, nil
	}
	var b strings.Builder
	n := total
	if n > listLimit {
		n = listLimit
	}
	for i := 0; i < n; i++ {
		e := entries[i]
		name := e.Name()
		if name == "." || name == ".." {
			continue
		}
		if e.IsDir() {
			fmt.Fprintf(&b, "dir  %s/\n", name)
			continue
		}
		size := "-"
		if fi, err := e.Info(); err == nil {
			size = fmt.Sprintf("%d", fi.Size())
		}
		fmt.Fprintf(&b, "file %s  %s\n", name, size)
	}
	if total > listLimit {
		fmt.Fprintf(&b, "(truncated, showing %d of %d entries)\n", listLimit, total)
	}
	return tools.ToolResult{Content: strings.TrimRight(b.String(), "\n")}, nil
}

// bizErrf 构造模型可见的业务失败（ToolResult.IsError，而非机制 error）。
func bizErrf(format string, a ...any) tools.ToolResult {
	return tools.ToolResult{Content: fmt.Sprintf(format, a...), IsError: true}
}

// fsTitle 卡片主标签：路径末段（read/write/replace 是文件，list 是目录）。
// 只取末段：卡片一行内要一眼认出目标，全路径太长。
func fsTitle(path string) string {
	base := filepath.Base(filepath.Clean(path))
	if base == "." || base == string(filepath.Separator) {
		return "(workspace)"
	}
	return base
}

// fsOp 卡片动作徽章：replace 的语义即"编辑"
func fsOp(action string) string {
	if action == "replace" {
		return "edit"
	}
	return action
}

func bizErr(err error) tools.ToolResult {
	return tools.ToolResult{Content: err.Error(), IsError: true}
}
