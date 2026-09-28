package session

import (
	"testing"
)

// 置顶契约：取最后一次置顶事件的状态；从未置顶返回 false。
func TestPinned(t *testing.T) {
	dir := t.TempDir()
	id := "s-20260928-140000"

	l, err := OpenLedger(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventSessionPinned, map[string]bool{"pinned": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventSessionPinned, map[string]bool{"pinned": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventSessionPinned, map[string]bool{"pinned": true}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	pinned, err := Pinned(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if !pinned {
		t.Fatal("Pinned = false, want true（取最后一次事件）")
	}
}

func TestPinned_NeverPinnedReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	id := "s-20260928-140001"

	l, err := OpenLedger(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventSessionRenamed, map[string]string{"title": "x"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	pinned, err := Pinned(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if pinned {
		t.Fatal("Pinned = true, want false（从未置顶）")
	}
}

// 最后活跃契约：账本文件修改时间；文件不存在（全新会话）返回 0。
func TestLastActive(t *testing.T) {
	dir := t.TempDir()
	id := "s-20260928-140002"

	// 未创建账本：0
	ms, err := LastActive(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if ms != 0 {
		t.Fatalf("LastActive = %d, want 0（文件不存在）", ms)
	}

	l, err := OpenLedger(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventSessionRenamed, map[string]string{"title": "x"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	ms, err = LastActive(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if ms <= 0 {
		t.Fatalf("LastActive = %d, want >0（账本已写入）", ms)
	}
}
