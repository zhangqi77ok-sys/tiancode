package fstool

import (
	"context"
	"encoding/json"
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
