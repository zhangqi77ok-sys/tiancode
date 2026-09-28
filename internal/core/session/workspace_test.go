package session

import (
	"testing"
)

// 会话归属工作区契约：取账本首个 workspace 事件；无事件返回空串（旧账本兼容）。
// 固定字节/固定路径，不依赖机器环境。
func TestWorkspace(t *testing.T) {
	dir := t.TempDir()
	id := "s-20260928-130000"

	l, err := OpenLedger(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventWorkspace, map[string]string{"path": "D:/proj/a"}); err != nil {
		t.Fatal(err)
	}
	// 中途切换工作区再记一次：归属仍取首个
	if _, err := l.Append(EventWorkspace, map[string]string{"path": "D:/proj/b"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	ws, err := Workspace(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if ws != "D:/proj/a" {
		t.Fatalf("Workspace = %q, want D:/proj/a（归属取首个）", ws)
	}
}

func TestWorkspace_NoEventReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	id := "s-20260928-130001"

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

	ws, err := Workspace(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if ws != "" {
		t.Fatalf("Workspace = %q, want empty（旧账本兼容）", ws)
	}
}
