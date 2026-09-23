// Package memory 负责“记忆器官”的上下文装配：把多轮会话历史压缩/拼装成模型可消费的上下文窗口。
// 该逻辑原散落在宿主层 app.go，现收归领域层，使宿主只负责“取历史、调函数”，不再内含领域规则。
package memory

import (
	"fmt"
	"strings"

	"tiancode/internal/llm"
	"tiancode/internal/session"
)

// PruneHistoricalOutput 对历史冗长工具输出进行原位折叠修剪，保留上下文拓扑与公共前缀哈希
func PruneHistoricalOutput(content string, maxChars int) string {
	if len(content) <= maxChars {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= 12 {
		return content[:maxChars] + "...\n[Output pruned for KV cache efficiency]"
	}
	// 保留前 8 行与后 2 行关键信息，中间折叠行数
	head := strings.Join(lines[:8], "\n")
	tail := strings.Join(lines[len(lines)-2:], "\n")
	prunedLines := len(lines) - 10
	return fmt.Sprintf("%s\n\n[... %d lines folded/pruned for KV cache efficiency ...]\n\n%s", head, prunedLines, tail)
}

// BuildConversationWindow 动态构建模型多轮会话上下文窗口
// 基于两级微创修剪策略 (In-Place Output Pruning)，维持严格单向追加 (Append-Only) 保证公共前缀 KV Cache
func BuildConversationWindow(systemPrompt string, history []session.SessionMessage, maxHistoryChars int) []llm.Message {
	conversation := []llm.Message{
		{Role: "system", Content: systemPrompt},
	}

	if len(history) == 0 {
		return conversation
	}

	// 1. 双阈值原位修剪历史冗长输出 (In-Place Output Pruning)
	// 对倒数 2 条以前的历史消息，单条若超过 1,000 字符原位折叠，保留前后骨架与角色拓扑
	processed := make([]llm.Message, len(history))
	totalChars := 0
	for i, m := range history {
		content := m.Content
		if len(history) > 3 && i < len(history)-2 && len(content) > 1000 {
			content = PruneHistoricalOutput(content, 1000)
		}
		processed[i] = llm.Message{
			Role:    m.Role,
			Content: content,
		}
		totalChars += len(content)
	}

	// 2. 若全量历史在预算内，保持严格正序单向追加 (Append-Only，100% 保持前缀 KV Cache)
	if totalChars <= maxHistoryChars {
		conversation = append(conversation, processed...)
		return conversation
	}

	// 3. 超极端情况（如数十轮巨型上下文），从头部安全削减早期轮次
	// 严格角色对齐原则：裁剪后首条消息必须是 "user" 角色（杜绝孤立 assistant 或 tool 导致大模型 400 报错）
	startIndex := 0
	for startIndex < len(processed)-2 && totalChars > maxHistoryChars {
		totalChars -= len(processed[startIndex].Content)
		startIndex++
	}

	for startIndex < len(processed)-1 && processed[startIndex].Role != "user" {
		startIndex++
	}

	conversation = append(conversation, processed[startIndex:]...)
	return conversation
}
