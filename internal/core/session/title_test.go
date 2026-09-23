package session

import "testing"

// 标题是账本事实：取最后一个 session_renamed 事件（重放即可恢复，无需第二事实源）。
func TestSessionTitle_ReplaysLatestRename(t *testing.T) {
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
	// 无重命名事件 → 空标题（UI 回退显示会话 ID）
	if title, err := SessionTitle(dir, "s1"); err != nil || title != "" {
		t.Fatalf("title = %q err = %v, want empty", title, err)
	}

	l2, err := OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"第一版", "第二版"} {
		if _, err := l2.Append(EventSessionRenamed, map[string]string{"title": want}); err != nil {
			t.Fatal(err)
		}
		if got, err := SessionTitle(dir, "s1"); err != nil || got != want {
			t.Fatalf("title = %q err = %v, want %q", got, err, want)
		}
	}
	if err := l2.Close(); err != nil {
		t.Fatal(err)
	}
	// 关闭后重放仍然可取（标题持久化在账本，不依赖内存态）
	if got, err := SessionTitle(dir, "s1"); err != nil || got != "第二版" {
		t.Fatalf("after close: title = %q err = %v", got, err)
	}
}

// 非法会话 ID 必须拒绝（与 DeleteSession/OpenLedger 同一道防线）。
func TestSessionTitle_RejectsBadID(t *testing.T) {
	if _, err := SessionTitle(t.TempDir(), "../x"); err == nil {
		t.Fatal("bad session id must be rejected")
	}
}
