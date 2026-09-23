package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tiancode/internal/core/session"
)

// C-APP-1：持久化失败（账本目录创建失败）必须上抛，禁止吞错。
func TestChatService_PersistErrorPropagates(t *testing.T) {
	// 用一个同名"文件"占据目录位，使 MkdirAll 必然失败
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := NewChatService(Config{
		DataDir: blocker,
		BaseURL: "http://127.0.0.1:1", // 不会真正发请求：持久化先于网络
		APIKey:  "k",
		Model:   "m",
		WorkDir: ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(context.Background(), "s1", "hi"); err == nil {
		t.Fatal("persist failure must propagate to caller")
	}
}

// 装配校验：缺关键配置必须在构造期失败（fail-fast），不留半装配实例。
func TestChatService_ConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"missing datadir", Config{BaseURL: "http://x", Model: "m", WorkDir: "."}},
		{"missing baseurl", Config{DataDir: t.TempDir(), Model: "m", WorkDir: "."}},
		{"missing model", Config{DataDir: t.TempDir(), BaseURL: "http://x", WorkDir: "."}},
		{"missing workdir", Config{DataDir: t.TempDir(), BaseURL: "http://x", Model: "m"}},
	}
	for _, tc := range cases {
		if _, err := NewChatService(tc.cfg); err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
	}
}

// Replay 只投影已确认锚点：被取消轮次的 delta 不进历史（与 agent 语义一致）。
func TestChatService_ReplayProjectsAnchors(t *testing.T) {
	dir := t.TempDir()
	l, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "u1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventAssistantDelta, map[string]any{"text": "partial", "thinking": ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventAssistantMsg, map[string]string{"text": "a1"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := NewChatService(Config{DataDir: dir, BaseURL: "http://x", APIKey: "k", Model: "m", WorkDir: "."})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() // Windows: 未关闭句柄会阻止 TempDir 清理
	msgs, err := s.Replay("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (delta 不投影)", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "u1" {
		t.Fatalf("msgs[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "a1" {
		t.Fatalf("msgs[1] = %+v", msgs[1])
	}
}
