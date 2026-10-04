package autorun

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 缺文件 = 0（关闭，不是错误）；正常值透传；硬封顶；负数归零；
// 坏文件必须报错——静默当 0 会让用户以为功能开着（与"静默把语气当空"同一类事故）。
func TestStore_Resolve(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "autorun.json")

	s := New(p)
	if got, err := s.Resolve(); err != nil || got != 0 {
		t.Fatalf("缺文件必须回落到关闭：%d %v", got, err)
	}

	writeFixture(t, p, `{"segments":2}`)
	if got, err := s.Resolve(); err != nil || got != 2 {
		t.Fatalf("正常值未生效：%d %v", got, err)
	}

	writeFixture(t, p, `{"segments":99}`)
	if got, err := s.Resolve(); err != nil || got != MaxSegments {
		t.Fatalf("必须硬封顶在 %d：%d %v", MaxSegments, got, err)
	}

	writeFixture(t, p, `{"segments":-3}`)
	if got, err := s.Resolve(); err != nil || got != 0 {
		t.Fatalf("负数必须归零：%d %v", got, err)
	}

	writeFixture(t, p, `坏 JSON`)
	if _, err := s.Resolve(); err == nil {
		t.Fatal("坏文件必须报错，不得静默当 0")
	}
}
