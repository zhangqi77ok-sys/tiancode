package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// 常规路径：无冲突时 temp+Sync+Rename 一次成功。
func TestWriteFileAtomic_CreatesAndOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")

	if err := WriteFileAtomic(path, []byte("v1"), 0o600); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "v1" {
		t.Fatalf("content = %q, want v1", got)
	}

	if err := WriteFileAtomic(path, []byte("v2"), 0o600); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "v2" {
		t.Fatalf("content = %q, want v2", got)
	}
}

// C-SES-5：Windows rename 冲突（目标被占用）→ 备份式替换回退成功，原数据不丢。
func TestWriteFileAtomic_RenameConflictFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("old-data"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 注入首次 rename 失败（模拟目标被占用时的 AccessDenied），第二次走真实 rename
	calls := 0
	failFirst := func(from, to string) error {
		calls++
		if calls == 1 {
			return errors.New("access denied: target in use")
		}
		return os.Rename(from, to)
	}

	if err := writeFileAtomicWith(path, []byte("new-data"), 0o600, failFirst); err != nil {
		t.Fatalf("fallback should succeed: %v", err)
	}
	if calls < 2 {
		t.Fatalf("rename calls = %d, want >= 2 (fail + backup-move + retry)", calls)
	}
	if got, _ := os.ReadFile(path); string(got) != "new-data" {
		t.Fatalf("content = %q, want new-data", got)
	}
	// 原数据不丢：.bak 保留上一版本
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("backup must exist: %v", err)
	}
	if string(bak) != "old-data" {
		t.Fatalf("backup content = %q, want old-data", bak)
	}
}

// 回退也失败（.bak 都移不动）→ 必须返回错误且原文件未损坏。
func TestWriteFileAtomic_AllFallbacksFailReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("old-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	alwaysFail := func(from, to string) error { return errors.New("in use") }
	if err := writeFileAtomicWith(path, []byte("new-data"), 0o600, alwaysFail); err == nil {
		t.Fatal("all renames failed: must return error")
	}
	if got, _ := os.ReadFile(path); string(got) != "old-data" {
		t.Fatalf("original content damaged: %q", got)
	}
}
