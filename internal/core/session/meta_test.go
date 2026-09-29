package session

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// 0.2.36 审计 R2：元数据读取绝不截断——回合进行中（尾部半行）刷新列表，
// 文件必须原样、半行仍在。此前的 OpenLedger 路径会 Truncate 掉半行，
// 让内存序号与文件内容错位（数据损坏）。
func TestReadMeta_NeverTruncatesHalfLine(t *testing.T) {
	dir := t.TempDir()
	id := "s-meta-halfline"
	l, err := OpenLedger(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventSessionRenamed, map[string]string{"title": "标题"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventWorkspace, map[string]string{"path": "D:/proj"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// 模拟回合进行中：追加一行未写完的半行（无换行结尾）
	path := filepath.Join(dir, id+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(`{"seq":3,"kind":"assistant_delta","data":{"text":"半`)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	m, err := ReadMeta(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "标题" || m.Workspace != "D:/proj" {
		t.Fatalf("元数据 = %+v", m)
	}
	if m.Skipped == 0 {
		t.Fatal("尾部半行应计入 Skipped（可观测）")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("ReadMeta 不得改动账本文件：%d → %d 字节（截断 = 数据损坏）", len(before), len(after))
	}
	if !bytes.Contains(after, []byte("半")) {
		t.Fatal("半行被截断")
	}
}

// 中间损坏行：跳过并计数，元数据照常（单条坏数据不牵连整次读取）。
func TestReadMeta_SkipsCorruptLineWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	id := "s-meta-corrupt"
	path := filepath.Join(dir, id+".jsonl")
	content := "" +
		`{"seq":1,"kind":"session_renamed","data":{"title":"好标题"}}` + "\n" +
		`{"seq":2,"kind":"broken` + "\n" + // 损坏行
		`{"seq":3,"kind":"workspace","data":{"path":"D:/x"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMeta(dir, id)
	if err != nil {
		t.Fatalf("坏行不应让整次读取失败：%v", err)
	}
	if m.Title != "好标题" || m.Workspace != "D:/x" {
		t.Fatalf("元数据 = %+v（坏行前后的事件都应生效）", m)
	}
	if m.Skipped != 1 {
		t.Fatalf("Skipped = %d, want 1", m.Skipped)
	}
}

// 归属取首个（含显式空）：纯对话会话必须保持空，不被后续事件套上目录。
func TestReadMeta_WorkspaceTakesFirstEvent(t *testing.T) {
	dir := t.TempDir()
	id := "s-meta-first"
	l, err := OpenLedger(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventWorkspace, map[string]string{"path": ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventWorkspace, map[string]string{"path": "D:/later"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMeta(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if m.Workspace != "" {
		t.Fatalf("Workspace = %q, want 空（首个事件显式空 = 纯对话，不被后续覆盖）", m.Workspace)
	}
}
