package main

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"tiancode/internal/config"
	"tiancode/internal/core/memory"
	"tiancode/internal/session"
)

func TestBuildConversationWindow_InPlacePruningAndDeterminism(t *testing.T) {
	systemPrompt := "你是 湉码 / tiancode 纯原生桌面智能体。"

	// 构造多轮会话，其中历史轮次包含超长输出 (模拟 grep 或读取长文件)
	longOutput := strings.Repeat("Error on line 42: invalid memory reference in runtime.\n", 50) // ~2,750 字符
	history := []session.SessionMessage{
		{
			ID:      "msg_1",
			Role:    "user",
			Content: "请分析 main.go 中的错误",
		},
		{
			ID:      "msg_2",
			Role:    "assistant",
			Content: longOutput,
		},
		{
			ID:      "msg_3",
			Role:    "user",
			Content: "请尝试修复它",
		},
		{
			ID:      "msg_4",
			Role:    "assistant",
			Content: "正在修复...",
		},
		{
			ID:      "msg_5",
			Role:    "user",
			Content: "继续下一步",
		},
	}

	// 运行 buildConversationWindow 两次，验证输出完全确定性且哈希相等
	conv1 := memory.BuildConversationWindow(systemPrompt, history, 32000)
	conv2 := memory.BuildConversationWindow(systemPrompt, history, 32000)

	if len(conv1) != 6 { // 1 system + 5 history
		t.Fatalf("Expected 6 messages, got %d", len(conv1))
	}

	// 验证 msg_2 (历史工具长输出) 已被原位折叠修剪，且保留前缀
	asstMsg := conv1[2]
	if !strings.Contains(asstMsg.Content, "lines folded/pruned for KV cache efficiency") {
		t.Fatalf("Expected msg_2 to be pruned in-place, got: %s", asstMsg.Content)
	}

	// 验证最新用户输入 (msg_5) 未被修剪
	lastMsg := conv1[5]
	if lastMsg.Content != "继续下一步" {
		t.Fatalf("Expected latest user message intact, got: %s", lastMsg.Content)
	}

	// 验证两次生成的全部消息序列 SHA-256 绝对一致
	h1 := sha256.Sum256([]byte(fmt.Sprintf("%v", conv1)))
	h2 := sha256.Sum256([]byte(fmt.Sprintf("%v", conv2)))
	if h1 != h2 {
		t.Fatalf("Conversation hash drifted across calls: %x vs %x", h1, h2)
	}
}

func TestAppendEnabledPolicies_DeterministicAlphabeticalOrder(t *testing.T) {
	skills1 := []config.SkillConfig{
		{Name: "zebra", Prompt: "Zebra policy", Enabled: true},
		{Name: "apple", Prompt: "Apple policy", Enabled: true},
		{Name: "banana", Prompt: "Banana policy", Enabled: true},
	}
	skills2 := []config.SkillConfig{
		{Name: "banana", Prompt: "Banana policy", Enabled: true},
		{Name: "zebra", Prompt: "Zebra policy", Enabled: true},
		{Name: "apple", Prompt: "Apple policy", Enabled: true},
	}

	rules1 := []config.RuleConfig{
		{Title: "rule-b", Content: "Do B", Enabled: true},
		{Title: "rule-a", Content: "Do A", Enabled: true},
	}
	rules2 := []config.RuleConfig{
		{Title: "rule-a", Content: "Do A", Enabled: true},
		{Title: "rule-b", Content: "Do B", Enabled: true},
	}

	out1 := appendEnabledPolicies("base", skills1, rules1)
	out2 := appendEnabledPolicies("base", skills2, rules2)

	if out1 != out2 {
		t.Fatalf("Policies output not deterministic across input permutations:\nOut1: %s\nOut2: %s", out1, out2)
	}

	// 确认按 apple -> banana -> zebra 排序
	idxA := strings.Index(out1, "apple")
	idxB := strings.Index(out1, "banana")
	idxZ := strings.Index(out1, "zebra")
	if !(idxA < idxB && idxB < idxZ) {
		t.Fatalf("Expected alphabetical order apple < banana < zebra in:\n%s", out1)
	}
}
