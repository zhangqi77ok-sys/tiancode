package gittool

import (
	"os"
	"path/filepath"
	"testing"
)

// StatusFiles/DiffFile（0.0.24 Git 面板）：结构化 porcelain、未跟踪单列、
// 干净仓库空切片、单文件 diff 含变更行。
func TestStatusFiles_UntrackedAndClean(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	// 未跟踪：?? 单列
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := StatusFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].Untracked || entries[0].Path != "a.txt" {
		t.Fatalf("未跟踪文件应单列：%+v", entries)
	}
	// 提交后干净：空切片
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "init")
	entries, err = StatusFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("干净仓库应为空切片：%+v", entries)
	}
}

func TestStatusFiles_ModifiedAndDiffFile(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := StatusFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Untracked || entries[0].Path != "b.go" {
		t.Fatalf("修改文件应进清单且非未跟踪：%+v", entries)
	}
	if entries[0].Y != "M" {
		t.Fatalf("工作区状态应为 M：%+v", entries[0])
	}
	diff, err := DiffFile(dir, "b.go")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(diff, "-old") || !contains(diff, "+new") {
		t.Fatalf("diff 应含增删行：%s", diff)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
