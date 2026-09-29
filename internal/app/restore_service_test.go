package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/session"
)

// 0.0.07：RestoreToolWrite——replace 后恢复回旧值；文件被外部改过时拒绝；
// 新建文件的恢复=删除该文件（只删这次创建的那个路径）。恢复事件追加进账本
// 且无配对 tool_call（derive 投影跳过，不进模型上下文）。
func TestRestoreToolWrite(t *testing.T) {
	ws := t.TempDir()
	s := newChannelService(t, Config{WorkDir: ws})
	defer s.Close()

	mkSession := func(id string) {
		l, err := s.ledgerFor(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.Append(session.EventWorkspace, map[string]string{"path": ws}); err != nil {
			t.Fatal(err)
		}
	}

	// --- replace 场景：恢复回替换前 ---
	mkSession("s-restore-1")
	mustWriteFile(t, ws, "code.txt", "line1\nline2\nline3\n")
	appendUndoResult(t, s, "s-restore-1", "c1", restore{
		Path: "code.txt", OldExists: true,
		OldContent: "line1\nline2\nline3\n", NewSHA256: sha256Of("line1\nLINE2\nline3\n"),
	})
	mustWriteFile(t, ws, "code.txt", "line1\nLINE2\nline3\n")
	msg, err := s.RestoreToolWrite("s-restore-1", "c1")
	if err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	if got := mustReadFile(t, ws, "code.txt"); got != "line1\nline2\nline3\n" {
		t.Fatalf("恢复后内容 = %q, want 替换前", got)
	}
	if !strings.Contains(msg, "已恢复") {
		t.Fatalf("返回文案 = %q", msg)
	}
	// 恢复事件可见（Replay 投影有这条卡）
	msgs, err := s.Replay("s-restore-1")
	if err != nil {
		t.Fatal(err)
	}
	if !anyToolMsg(msgs, "已恢复 code.txt") {
		t.Fatalf("恢复事件必须可见：%+v", msgs)
	}

	// --- 文件被外部改过：拒绝恢复且文件保持被改后的样子 ---
	mustWriteFile(t, ws, "code.txt", "用户后来改的内容")
	if _, err := s.RestoreToolWrite("s-restore-1", "c1"); err == nil {
		t.Fatal("文件被外部改过后必须拒绝恢复")
	}
	if got := mustReadFile(t, ws, "code.txt"); got != "用户后来改的内容" {
		t.Fatalf("拒绝恢复后文件不得被碰：%q", got)
	}

	// --- 新建文件：恢复 = 删除该路径 ---
	mkSession("s-restore-2")
	appendUndoResult(t, s, "s-restore-2", "c2", restore{
		Path: "created.txt", OldExists: false, NewSHA256: sha256Of("new content"),
	})
	mustWriteFile(t, ws, "created.txt", "new content")
	if _, err := s.RestoreToolWrite("s-restore-2", "c2"); err != nil {
		t.Fatalf("新建文件恢复失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("新建文件恢复后应被删除，stat err=%v", err)
	}

	// --- 无恢复数据：显式报错 ---
	mkSession("s-restore-3")
	if _, err := s.RestoreToolWrite("s-restore-3", "no-such"); err == nil {
		t.Fatal("没有 undo 数据必须报错")
	}
}

// ---- helpers ----

func mustWriteFile(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustReadFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sha256Of(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// appendUndoResult 按agent 落账形态追加一条带 undo 的 tool_result（callID=call）。
func appendUndoResult(t *testing.T, s *ChatService, sessionID, call string, u restore) {
	t.Helper()
	l, err := s.ledgerFor(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventToolCall, map[string]string{
		"id": call, "name": "fs", "arguments": `{"action":"write"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventToolResult, map[string]any{
		"id": call, "name": "fs", "content": "written", "is_error": false,
		"title": u.Path, "op": "write", "undo": json.RawMessage(raw),
	}); err != nil {
		t.Fatal(err)
	}
}

func anyToolMsg(msgs []ChatMessage, want string) bool {
	for _, m := range msgs {
		if m.Role == "tool" && strings.Contains(m.Content, want) {
			return true
		}
	}
	return false
}
