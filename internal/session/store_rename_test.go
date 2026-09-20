package session_test

import (
	"os"
	"testing"
	"tiancode/internal/session"
)

func TestStoreRename(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tiancode_test_rename_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	origHome := os.Getenv("USERPROFILE")
	os.Setenv("USERPROFILE", tempDir)
	defer os.Setenv("USERPROFILE", origHome)

	store, err := session.NewStore()
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	testSess := session.ChatSession{
		ID:        "sess_rename_001",
		Title:     "原始标题",
		Workspace: tempDir,
		CreatedAt: 1000,
		UpdatedAt: 1000,
		Messages: []session.SessionMessage{
			{ID: "m1", Role: "user", Content: "hello"},
		},
	}
	if err := store.Save(testSess); err != nil {
		t.Fatalf("failed to save session: %v", err)
	}

	// 执行重命名
	newTitle := "新会话标题_已修改"
	if err := store.Rename("sess_rename_001", newTitle); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}

	// 重新读取验证
	updated, err := store.Get("sess_rename_001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if updated.Title != newTitle {
		t.Errorf("expected title %q, got %q", newTitle, updated.Title)
	}

	// 验证在列表元数据中也更新
	metas := store.List(tempDir)
	var found bool
	for _, m := range metas {
		if m.ID == "sess_rename_001" {
			found = true
			if m.Title != newTitle {
				t.Errorf("metadata title expected %q, got %q", newTitle, m.Title)
			}
		}
	}
	if !found {
		t.Errorf("session not found in list")
	}

	// 测试重命名不存在的会话
	if err := store.Rename("not_exist_sess", "xxx"); err == nil {
		t.Errorf("expected error when renaming non-existent session, got nil")
	}
}
