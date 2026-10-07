package fstool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 旧文件超过写入上限时，界面不能拿到一份空的撤销快照——按它恢复会把文件写成空。
func TestWrite_OversizeOldContentHasNoUndo(t *testing.T) {
	dir := t.TempDir()
	tool := New(dir)
	ctx := context.Background()

	first, err := tool.Execute(ctx, mustJSON(t, map[string]any{
		"action": "write", "path": "big.txt", "content": "seed",
	}))
	if err != nil || first.IsError {
		t.Fatalf("新建失败：%v %s", err, first.Content)
	}

	huge := make([]byte, maxWriteBytes+1)
	for i := range huge {
		huge[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), huge, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := tool.Execute(ctx, mustJSON(t, map[string]any{
		"action": "write", "path": "big.txt", "content": "next",
	}))
	if err != nil || res.IsError {
		t.Fatalf("覆盖失败：%v %s", err, res.Content)
	}
	if res.Undo != nil {
		t.Fatal("超限旧内容不得挂撤销快照（恢复会写成空文件）")
	}
	if !strings.Contains(res.UndoNote, "无法恢复") {
		t.Fatalf("UndoNote = %q", res.UndoNote)
	}
	got, err := os.ReadFile(filepath.Join(dir, "big.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "next" {
		t.Fatalf("文件内容 = %q", got)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
