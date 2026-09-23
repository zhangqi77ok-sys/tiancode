package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 删除会话：删掉账本文件使 ListSessions 不再列出。
func TestDeleteSession_RemovesLedger(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventUserMessage, map[string]string{"text": "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if ids, _ := ListSessions(dir); len(ids) != 1 {
		t.Fatalf("ids = %v, want [s1]", ids)
	}

	if err := DeleteSession(dir, "s1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ids, _ := ListSessions(dir); len(ids) != 0 {
		t.Fatalf("ids after delete = %v, want empty", ids)
	}
	// 幂等：用户连点两下/重试不该看到报错（"删不掉"的观感比静默无变更更糟）
	if err := DeleteSession(dir, "s1"); err != nil {
		t.Fatalf("second delete must be no-op, got %v", err)
	}
}

// 安全红线：会话 ID 来自 UI，含路径分隔符时必须拒绝——
// 否则 "..\\config" 这类输入能删掉工作区外的任意文件。
func TestDeleteSession_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"", ".", "..", "../victim", `..\victim`, "a/b", `a\b`, "s1/../../victim"} {
		if err := DeleteSession(dir, id); err == nil {
			t.Fatalf("id %q must be rejected", id)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file must survive: %v", err)
	}
	if !strings.Contains(DeleteSession(dir, "a/b").Error(), "会话") {
		t.Fatal("error message should be user-readable (mention 会话)")
	}
}
