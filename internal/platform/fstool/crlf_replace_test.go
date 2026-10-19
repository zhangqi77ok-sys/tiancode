package fstool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func runFS(t *testing.T, tool *Tool, args map[string]any) (string, bool) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return res.Content, res.IsError
}

// P1 体检修复：read 输出去掉了 \r，模型照抄的多行 target 只含 \n——CRLF 文件里
// 曾经永远零匹配。现在按文件换行风格匹配，replacement 同步写成 CRLF（不混入 LF）。
func TestReplace_MultilineTargetOnCRLFFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("line1\r\nline2\r\nline3\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := New(dir)
	if out, isErr := runFS(t, tool, map[string]any{
		"action": "replace", "path": "a.txt", "target": "line1\nline2", "replacement": "A\nB",
	}); isErr {
		t.Fatalf("CRLF 文件的多行替换应成功：%s", out)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "A\r\nB\r\nline3\r\n" {
		t.Fatalf("替换结果应保持 CRLF：%q", got)
	}

	// edits 多段形态同样适配
	if out, isErr := runFS(t, tool, map[string]any{
		"action": "replace", "path": "a.txt",
		"edits": []map[string]string{{"target": "B\nline3", "replacement": "C\nD"}},
	}); isErr {
		t.Fatalf("edits 在 CRLF 文件上应成功：%s", out)
	}
	got, _ = os.ReadFile(p)
	if string(got) != "A\r\nC\r\nD\r\n" {
		t.Fatalf("edits 结果应保持 CRLF：%q", got)
	}
}

// LF 文件与原样可匹配的情况行为不变：不做任何换行转换。
func TestReplace_LFFileUnchangedBehavior(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(p, []byte("x\ny\nz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := New(dir)
	if out, isErr := runFS(t, tool, map[string]any{
		"action": "replace", "path": "b.txt", "target": "x\ny", "replacement": "1\n2",
	}); isErr {
		t.Fatalf("LF 替换应成功：%s", out)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "1\n2\nz\n" {
		t.Fatalf("LF 文件不应引入 CRLF：%q", got)
	}
}
