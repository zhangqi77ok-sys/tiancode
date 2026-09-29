package app

import (
	"os"
	"path/filepath"
	"testing"
)

// 工作区切换：合法目录生效并持久于服务状态；非法输入全部拒绝。
func TestChatService_SetWorkspace(t *testing.T) {
	s := newChannelService(t, Config{})
	dir := t.TempDir()

	if err := s.SetWorkspace(dir); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if got := s.Workspace(); got != normalizeWorkspace(dir) {
		t.Fatalf("Workspace() = %q, want %q", got, dir)
	}

	// 不存在的目录
	if err := s.SetWorkspace(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("missing dir must be rejected")
	}
	// 存在但不是目录
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkspace(file); err == nil {
		t.Fatal("file path must be rejected")
	}
	// 被拒绝的调用不得改变当前工作区（否则 UI 与实际不一致）
	if got := s.Workspace(); got != normalizeWorkspace(dir) {
		t.Fatalf("failed switch must not mutate workspace: %q", got)
	}

	// 空白 = 退出工作区（0.2.18 语义：空串清除；纯对话模式）——TrimSpace 后为空即退出
	if err := s.SetWorkspace("   "); err != nil {
		t.Fatalf("空白应视为退出工作区：%v", err)
	}
	if got := s.Workspace(); got != "" {
		t.Fatalf("退出后 Workspace() = %q, want 空", got)
	}
}
