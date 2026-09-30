package app

import (
	"os"
	"path/filepath"
	"testing"

	"tiancode/internal/core/session"
)

// 0.0.11：会话内路径解析必须以**这场对话**的工作区为根——默认根（"下一场新对话"）
// 里即使躺着同名文件也不得命中（切换工作区后旧会话的"显示/打开"曾指到别的目录）。
func TestResolveSessionPath_UsesSessionWorkspace(t *testing.T) {
	rootA := t.TempDir() // 这场对话的工作区
	rootB := t.TempDir() // 顶栏"下一场新对话"的默认根（最容易踩错的那个）
	s := newChannelService(t, Config{WorkDir: rootB})
	defer s.Close()

	// 两边都放同名文件：命中 B 的那份就是回归
	for _, dir := range []string{rootA, rootB} {
		if err := os.WriteFile(filepath.Join(dir, "same.txt"), []byte(dir), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(rootA, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := s.ledgerFor("s-path")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": rootA}); err != nil {
		t.Fatal(err)
	}

	abs, isDir, err := s.ResolveSessionPath("s-path", "same.txt")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	want := filepath.Join(normalizeWorkspace(rootA), "same.txt")
	if abs != want {
		t.Fatalf("必须按会话工作区解析：got %q want %q（默认根 %q 里也有同名文件）", abs, want, rootB)
	}
	if isDir {
		t.Fatal("文件被当成目录")
	}
	// 目录：isDir=true（调用方据此"直接打开目录本身"）
	if _, d, err := s.ResolveSessionPath("s-path", "sub"); err != nil || !d {
		t.Fatalf("目录解析：isDir=%v err=%v", d, err)
	}

	// 显式错误：越界 / 不存在 / 无工作区
	if _, _, err := s.ResolveSessionPath("s-path", filepath.Join("..", "escape.txt")); err == nil {
		t.Fatal("越出工作区必须报错")
	}
	if _, _, err := s.ResolveSessionPath("s-path", "missing.txt"); err == nil {
		t.Fatal("文件不存在必须报错")
	}
	if _, _, err := s.ResolveSessionPath("s-chat-only", "a.txt"); err == nil {
		t.Fatal("没有工作区的会话必须报错")
	}
}
