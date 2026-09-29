package fstool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTool(t *testing.T) *Tool {
	t.Helper()
	return New(t.TempDir())
}

func mustArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustWrite(t *testing.T, tool *Tool, path, content string) {
	t.Helper()
	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action": "write", "path": path, "content": content,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("write failed: %s", res.Content)
	}
}

// C-FS-1：write 原子落盘——写后读一致、覆盖写生效、无临时文件残留。
func TestFSWrite_Atomic(t *testing.T) {
	tool := newTool(t)

	mustWrite(t, tool, "a.txt", "hello")
	if got, err := os.ReadFile(filepath.Join(tool.Root(), "a.txt")); err != nil || string(got) != "hello" {
		t.Fatalf("content = %q, %v", got, err)
	}

	mustWrite(t, tool, "a.txt", "world")
	if got, _ := os.ReadFile(filepath.Join(tool.Root(), "a.txt")); string(got) != "world" {
		t.Fatalf("overwrite content = %q, want world", got)
	}

	entries, err := os.ReadDir(tool.Root())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp file leftover: %s", e.Name())
		}
	}
}

// C-FS-2：replace 多处匹配 → 报错且文件零修改；显式 allow_multiple 才放行。
func TestFSReplace_MultiMatchBlocks(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "code.txt", "aXa")

	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action": "replace", "path": "code.txt", "target": "a", "replacement": "b",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("multi-match replace must be blocked")
	}
	if got, _ := os.ReadFile(filepath.Join(tool.Root(), "code.txt")); string(got) != "aXa" {
		t.Fatalf("file modified on blocked replace: %q", got)
	}

	// 显式允许多处 → 全部替换
	res, err = tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action": "replace", "path": "code.txt", "target": "a", "replacement": "b", "allow_multiple": true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("allow_multiple replace failed: %s", res.Content)
	}
	if got, _ := os.ReadFile(filepath.Join(tool.Root(), "code.txt")); string(got) != "bXb" {
		t.Fatalf("content = %q, want bXb", got)
	}
}

// C-FS-3：replace 零匹配 → 报错且文件零修改。
func TestFSReplace_NoMatchBlocks(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "code.txt", "abc")

	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action": "replace", "path": "code.txt", "target": "zzz", "replacement": "y",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("no-match replace must be blocked")
	}
	if got, _ := os.ReadFile(filepath.Join(tool.Root(), "code.txt")); string(got) != "abc" {
		t.Fatalf("file modified on blocked replace: %q", got)
	}
}

// C-FS-4：路径越界（../、绝对路径）→ 拒绝执行且不产生文件。
func TestFSWrite_PathEscapeRejected(t *testing.T) {
	tool := newTool(t)
	parent := filepath.Dir(tool.Root())

	cases := []string{"../evil.txt", "sub/../../evil.txt", filepath.Join(parent, "evil.txt")}
	for _, p := range cases {
		res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
			"action": "write", "path": p, "content": "evil",
		}))
		if err != nil {
			t.Fatalf("path %q: Execute must not return mechanism error: %v", p, err)
		}
		if !res.IsError {
			t.Fatalf("path %q: escape must be rejected", p)
		}
	}
	// 越界目标不得存在
	if _, err := os.Stat(filepath.Join(parent, "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("escaped file was created outside workspace")
	}
}

// read：存在返回内容；不存在 → IsError（模型可见的业务失败）。
func TestFSRead(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "a.txt", "content-x")

	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "read", "path": "a.txt"}))
	if err != nil || res.IsError {
		t.Fatalf("read failed: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "content-x") {
		t.Fatalf("read content = %q", res.Content)
	}

	res, err = tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "read", "path": "nope.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("read missing file must be a business error")
	}
}

// C-FS-5：list 越界或非目录 → IsError 且工作区零修改。
func TestFSList_RejectsEscapeAndNonDir(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "a.txt", "x")
	parent := filepath.Dir(tool.Root())

	for _, p := range []string{"../evil", filepath.Join(parent, "x")} {
		res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "list", "path": p}))
		if err != nil {
			t.Fatalf("path %q: mechanism error %v", p, err)
		}
		if !res.IsError {
			t.Fatalf("path %q: escape must be IsError", p)
		}
	}
	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "list", "path": "a.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("list on file must be IsError")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "evil" {
			t.Fatal("list escape created parent entry")
		}
	}
}

// C-FS-6：list 最多 500 条，超出截断并标注总数。
func TestFSList_OutputBounded(t *testing.T) {
	tool := newTool(t)
	for i := 0; i < 510; i++ {
		mustWrite(t, tool, filepath.Join("f", fmt.Sprintf("%04d.txt", i)), "x")
	}
	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "list", "path": "f"}))
	if err != nil || res.IsError {
		t.Fatalf("list: %v %s", err, res.Content)
	}
	lines := strings.Split(strings.TrimRight(res.Content, "\n"), "\n")
	if len(lines) != 501 { // 500 entries + truncated line
		t.Fatalf("lines = %d, want 501", len(lines))
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "truncated") || !strings.Contains(last, "510") {
		t.Fatalf("truncation line = %q", last)
	}
}

// C-FS-7：list 只列下一层。
func TestFSList_NonRecursive(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "sub/nested.txt", "x")
	mustWrite(t, tool, "top.txt", "y")
	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "list"}))
	if err != nil || res.IsError {
		t.Fatalf("list: %v %s", err, res.Content)
	}
	if strings.Contains(res.Content, "nested.txt") {
		t.Fatalf("recursive leak: %s", res.Content)
	}
	if !strings.Contains(res.Content, "top.txt") || !strings.Contains(res.Content, "sub/") {
		t.Fatalf("missing top entries: %s", res.Content)
	}
}

// 0.2.37 审计：大文件必须先查大小再读——replace 超 maxWriteBytes 直接拒绝（不打开
// 全文）；write 对超大旧文件省略 diff（不为预览把整个文件读进内存），照常写入。
// 用 Truncate 稀疏化构造超大文件，避免真实写 32MB。
func TestFSOversized_ReplaceRejectsWriteSkipsDiff(t *testing.T) {
	tool := newTool(t)
	p := filepath.Join(tool.Root(), "huge.txt")

	// --- replace：超限拒绝，且文件未被打开/修改 ---
	mustWrite(t, tool, "huge.txt", "old-content needle")
	f, err := os.OpenFile(p, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxWriteBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action": "replace", "path": "huge.txt", "target": "needle", "replacement": "x",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("oversized replace must be rejected: %s", res.Content)
	}
	if !strings.Contains(res.Content, "too large to replace") {
		t.Fatalf("error must name the cap: %s", res.Content)
	}
	if info, _ := os.Stat(p); info == nil || info.Size() != maxWriteBytes+1 {
		t.Fatalf("rejected replace must not touch file: size=%v", info)
	}

	// --- write：同样超大旧文件，写入成功、diff 省略、内容正确 ---
	res, err = tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action": "write", "path": "huge.txt", "content": "fresh small content",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("write over oversized old file must succeed: %s", res.Content)
	}
	if res.Diff != "" {
		t.Fatalf("diff must be omitted for oversized old file, got %d bytes", len(res.Diff))
	}
	if got, _ := os.ReadFile(p); string(got) != "fresh small content" {
		t.Fatalf("content = %q", got)
	}

	// --- 小文件回归：write 仍有 diff（省略只发生在超大场景） ---
	res, err = tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action": "write", "path": "small.txt", "content": "v2",
	}))
	if err != nil || res.IsError {
		t.Fatalf("small write: %v %s", err, res.Content)
	}
	if res.Diff == "" {
		t.Fatal("small file write must keep diff")
	}
}
