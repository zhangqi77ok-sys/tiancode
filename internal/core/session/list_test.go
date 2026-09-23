package session

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// ListSessions：空目录/不存在目录返回空；只认 .jsonl；按文件名排序。
func TestListSessions(t *testing.T) {
	dir := t.TempDir()

	if ids, err := ListSessions(dir); err != nil || len(ids) != 0 {
		t.Fatalf("empty dir: ids=%v err=%v", ids, err)
	}
	if ids, err := ListSessions(filepath.Join(dir, "nope")); err != nil || ids != nil {
		t.Fatalf("missing dir: ids=%v err=%v", ids, err)
	}

	for _, id := range []string{"b-session", "a-session"} {
		l, err := OpenLedger(dir, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// 非 .jsonl 文件必须被忽略
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	ids, err := ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a-session", "b-session"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}
