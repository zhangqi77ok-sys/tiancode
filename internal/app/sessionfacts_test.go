package app

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"tiancode/internal/core/agent"
)

// 0.0.06：每个执行步骤的系统说明必须带三行环境事实——操作系统、实际 shell
// （Windows 为 cmd.exe，不是 PowerShell）、本会话工作区绝对路径；空工作区写明
// 纯对话。三行是事实不是守则；绝不包含任何密钥。
func TestChatService_SessionFactsPreface(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	ag := agent.NewLoop(nil, "m", nil)

	// 有工作区：三行齐 + 绝对路径在场
	root := t.TempDir()
	if err := s.applyExtensionPreface(context.Background(), ag, root); err != nil {
		t.Fatal(err)
	}
	p := ag.Preface()
	if !strings.Contains(p, "操作系统") {
		t.Fatalf("缺操作系统行：%q", p)
	}
	if runtime.GOOS == "windows" && !strings.Contains(p, "cmd.exe") {
		t.Fatalf("Windows 必须写明实际 shell 是 cmd.exe：%q", p)
	}
	if runtime.GOOS != "windows" && !strings.Contains(p, "sh") {
		t.Fatalf("Unix 必须写明实际 shell：%q", p)
	}
	if !strings.Contains(p, root) {
		t.Fatalf("缺工作区绝对路径 %q：%q", root, p)
	}
	lower := strings.ToLower(p)
	for _, secret := range []string{"sk-", "api_key", "apikey", "bearer "} {
		if strings.Contains(lower, secret) {
			t.Fatalf("preface 不得包含密钥类内容（%s）：%q", secret, p)
		}
	}

	// 空工作区：写明纯对话、没有本地文件工具
	ag2 := agent.NewLoop(nil, "m", nil)
	if err := s.applyExtensionPreface(context.Background(), ag2, ""); err != nil {
		t.Fatal(err)
	}
	p2 := ag2.Preface()
	if !strings.Contains(p2, "纯对话，没有本地文件工具") {
		t.Fatalf("空工作区必须写明纯对话：%q", p2)
	}
	if strings.Contains(p2, "本会话工作区：D:") {
		t.Fatalf("空工作区不得出现具体路径：%q", p2)
	}
}
