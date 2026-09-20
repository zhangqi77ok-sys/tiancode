package fs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"tiancode/internal/core/sandbox"
)

func TestFSTool_Replace_SingleOccurrence(t *testing.T) {
	tempDir := t.TempDir()
	sb, err := sandbox.NewSandbox(tempDir)
	if err != nil {
		t.Fatalf("failed to create sandbox: %v", err)
	}
	sm := sandbox.NewSnapshotManager(tempDir)
	tool := NewTool(sb, sm)

	// 先写入初始文件
	initialContent := "func main() {\n\tprintln(\"hello world\")\n}\n"
	writeArgs, _ := json.Marshal(map[string]string{
		"action":  "write",
		"path":    "main.go",
		"content": initialContent,
	})
	if res, err := tool.Execute(context.Background(), writeArgs); err != nil || res.IsError {
		t.Fatalf("write failed: %v, content: %s", err, res.Content)
	}

	// 局部精准替换
	replaceArgs, _ := json.Marshal(map[string]any{
		"action":              "replace",
		"path":                "main.go",
		"target_content":      "println(\"hello world\")",
		"replacement_content": "println(\"hello tiancode\")",
	})
	res, err := tool.Execute(context.Background(), replaceArgs)
	if err != nil || res.IsError {
		t.Fatalf("replace failed: %v, content: %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "replaced successfully") {
		t.Errorf("expected success message, got: %s", res.Content)
	}

	// 验证最终读取
	readArgs, _ := json.Marshal(map[string]string{"action": "read", "path": "main.go"})
	readRes, _ := tool.Execute(context.Background(), readArgs)
	expected := "func main() {\n\tprintln(\"hello tiancode\")\n}\n"
	if readRes.Content != expected {
		t.Errorf("expected content:\n%s\ngot:\n%s", expected, readRes.Content)
	}
}

func TestFSTool_Replace_NotFound(t *testing.T) {
	tempDir := t.TempDir()
	sb, _ := sandbox.NewSandbox(tempDir)
	tool := NewTool(sb, nil)

	_ = sb.AtomicWriteFile("demo.txt", []byte("alpha beta gamma"))

	replaceArgs, _ := json.Marshal(map[string]any{
		"action":              "replace",
		"path":                "demo.txt",
		"target_content":      "nonexistent",
		"replacement_content": "delta",
	})
	res, err := tool.Execute(context.Background(), replaceArgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error when target_content is not found")
	}
	if !strings.Contains(res.Content, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", res.Content)
	}
}

func TestFSTool_Replace_MultipleOccurrencesGuard(t *testing.T) {
	tempDir := t.TempDir()
	sb, _ := sandbox.NewSandbox(tempDir)
	tool := NewTool(sb, nil)

	_ = sb.AtomicWriteFile("demo.txt", []byte("foo bar foo baz"))

	// 1. 未指定 allow_multiple，应当报错阻断
	replaceArgs, _ := json.Marshal(map[string]any{
		"action":              "replace",
		"path":                "demo.txt",
		"target_content":      "foo",
		"replacement_content": "qux",
	})
	res, _ := tool.Execute(context.Background(), replaceArgs)
	if !res.IsError {
		t.Errorf("expected error for multiple occurrences without allow_multiple")
	}
	if !strings.Contains(res.Content, "matched 2 times") {
		t.Errorf("expected multiple matches warning, got: %s", res.Content)
	}

	// 2. 指定 allow_multiple: true，应当成功替换全部
	replaceArgsMultiple, _ := json.Marshal(map[string]any{
		"action":              "replace",
		"path":                "demo.txt",
		"target_content":      "foo",
		"replacement_content": "qux",
		"allow_multiple":      true,
	})
	res2, err := tool.Execute(context.Background(), replaceArgsMultiple)
	if err != nil || res2.IsError {
		t.Fatalf("expected success with allow_multiple, got: %s", res2.Content)
	}

	readData, _ := sb.SafeReadFile("demo.txt")
	if string(readData) != "qux bar qux baz" {
		t.Errorf("expected 'qux bar qux baz', got: '%s'", string(readData))
	}
}

func TestFSTool_Replace_ScopedLineRange(t *testing.T) {
	tempDir := t.TempDir()
	sb, _ := sandbox.NewSandbox(tempDir)
	tool := NewTool(sb, nil)

	content := "line 1: same\nline 2: target\nline 3: same\nline 4: target\nline 5: end"
	_ = sb.AtomicWriteFile("scoped.txt", []byte(content))

	// 仅替换第 3 至 5 行内的 target
	replaceArgs, _ := json.Marshal(map[string]any{
		"action":              "replace",
		"path":                "scoped.txt",
		"start_line":          3,
		"end_line":            5,
		"target_content":      "target",
		"replacement_content": "MODIFIED",
	})
	res, err := tool.Execute(context.Background(), replaceArgs)
	if err != nil || res.IsError {
		t.Fatalf("expected scoped replace success, got: %s", res.Content)
	}

	readData, _ := sb.SafeReadFile("scoped.txt")
	expected := "line 1: same\nline 2: target\nline 3: same\nline 4: MODIFIED\nline 5: end"
	if string(readData) != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, string(readData))
	}
}
