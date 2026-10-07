package app

import (
	"context"
	"os"
	"path/filepath"
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
	if err := s.applyExtensionPreface(context.Background(), ag, root, false); err != nil {
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
	if err := s.applyExtensionPreface(context.Background(), ag2, "", false); err != nil {
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

// 0.0.42（C-PRE-1）：工作区根 AGENTS.md 作为项目规则注入；无/空文件零噪声；
// 超上限头尾保留并注明节选。
func TestSessionFacts_AgentsRules(t *testing.T) {
	root := t.TempDir()
	if got := sessionFacts(root); strings.Contains(got, "项目规则") {
		t.Fatalf("无 AGENTS.md 不应出现规则段：%q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"),
		[]byte("# 规则\n- 构建用 npm run build\n- 禁止改 vendor"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := sessionFacts(root)
	if !strings.Contains(got, "## 项目规则") || !strings.Contains(got, "禁止改 vendor") {
		t.Fatalf("AGENTS.md 应注入：%q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sessionFacts(root); strings.Contains(got, "项目规则") {
		t.Fatalf("空 AGENTS.md 不应出现规则段：%q", got)
	}

	big := strings.Repeat("A", agentsRulesLimit+1000) + "尾部标记" + strings.Repeat("B", 500)
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	got = sessionFacts(root)
	if !strings.Contains(got, "已节选") || !strings.Contains(got, "尾部标记") {
		t.Fatalf("超限应节选并保尾部：%q", got[max(0, len(got)-160):])
	}
	if len(got) > agentsRulesLimit+600 { // 环境三行 + 上限正文 + 标注的粗上界
		t.Fatalf("注入体量应受限：len=%d", len(got))
	}
}
