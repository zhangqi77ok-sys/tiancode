// 目录树过滤测试（0.0.21）：右栏「目录」与 search / @ 引用同一份忽略清单——
// 0.0.12 登记了"filetree 承载忽略策略"但实际漏接，这里锁死。
package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListWorkspaceDir_FiltersIgnoredDirs(t *testing.T) {
	s, ws := newMiniService(t)
	seedSession(t, s, "s-tree", ws)

	mustMkdir := func(rel string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(ws, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustMkdir("node_modules")
	mustMkdir(".git")
	mustMkdir("internal")
	if err := os.WriteFile(filepath.Join(ws, "node_modules.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err) // 同名文件不误杀
	}

	entries, err := s.ListWorkspaceDir("s-tree", "")
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name] = true
	}
	// 精确匹配（子串匹配会被 node_modules.txt 误命中）
	for _, ignored := range []string{"node_modules", ".git"} {
		if names[ignored] {
			t.Fatalf("目录树输出含忽略目录：%s", ignored)
		}
	}
	for _, want := range []string{"internal", "node_modules.txt"} {
		if !names[want] {
			t.Fatalf("正常条目 %s 丢失：%v", want, names)
		}
	}
}
