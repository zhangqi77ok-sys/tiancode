package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tiancode/internal/session"
)

func TestExpandMentionedFiles_ChineseAndSpaceless(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "main.go")
	_ = os.WriteFile(filePath, []byte("package main\nfunc Run() {}\n"), 0644)

	// 测试中文无空格前缀与后缀的句子
	prompt := "请结合@main.go中的逻辑帮我实现新功能，谢谢！"
	expanded := expandMentionedFiles(tempDir, nil, prompt)

	if !strings.Contains(expanded, "--- [引用文件内容: main.go] ---") {
		t.Errorf("expected expanded mention for main.go in Chinese prompt, got:\n%s", expanded)
	}
	if !strings.Contains(expanded, "func Run() {}") {
		t.Errorf("expected file content to be embedded, got:\n%s", expanded)
	}
}

func TestBuildConversationWindow_UserRoleAlignment(t *testing.T) {
	systemPrompt := "You are a helpful coding assistant."
	history := []session.SessionMessage{
		{Role: "user", Content: "user prompt 1 with lots of padding content to test capacity " + strings.Repeat("A", 1000)},
		{Role: "assistant", Content: "assistant reply 1 with lots of padding content " + strings.Repeat("B", 1000)},
		{Role: "tool", Content: "tool result 1 " + strings.Repeat("C", 1000)},
		{Role: "user", Content: "user prompt 2"},
		{Role: "assistant", Content: "assistant reply 2"},
	}

	// 强制使用极小的 maxHistoryChars 触发裁剪
	conv := buildConversationWindow(systemPrompt, history, 1200)

	// 系统消息在首位
	if len(conv) == 0 || conv[0].Role != "system" {
		t.Fatalf("first message must be system")
	}

	// 裁剪后的第一条对话消息必须是 user 角色！
	if len(conv) > 1 && conv[1].Role != "user" {
		t.Errorf("first message after system must be user role to prevent API 400 error, got role: %s", conv[1].Role)
	}
}
