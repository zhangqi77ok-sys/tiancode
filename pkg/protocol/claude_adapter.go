package protocol

import (
	"encoding/json"
	"fmt"

	v1 "tiancode/pkg/plugin/v1"
)

// ClaudeMessage Anthropic 请求消息
type ClaudeMessage struct {
	Role    string        `json:"role"` // 仅允许 "user" 或 "assistant"
	Content []ClaudeBlock `json:"content"`
}

// ClaudeBlock 内容块
type ClaudeBlock struct {
	Type         string         `json:"type"` // "text", "thinking", "tool_use", "tool_result"
	Text         string         `json:"text,omitempty"`
	Thinking     string         `json:"thinking,omitempty"`
	ID           string         `json:"id,omitempty"` // tool_use ID
	Name         string         `json:"name,omitempty"`
	Input        any            `json:"input,omitempty"`
	ToolUseID    string         `json:"tool_use_id,omitempty"` // tool_result 关联 ID
	Content      string         `json:"content,omitempty"`     // tool_result 结果内容
	IsError      bool           `json:"is_error,omitempty"`
	CacheControl *ClaudeCacheCtrl `json:"cache_control,omitempty"` // Prompt Caching
}

type ClaudeCacheCtrl struct {
	Type string `json:"type"` // "ephemeral"
}

// ClaudeTool Anthropic 工具定义
type ClaudeTool struct {
	Name         string           `json:"name"`
	Description  string           `json:"description,omitempty"`
	InputSchema  any              `json:"input_schema"`
	CacheControl *ClaudeCacheCtrl `json:"cache_control,omitempty"`
}

// ConvertToolsToClaude 将 ToolDefinition 列表转换为 Claude 格式，并在最后一个工具上挂载 Breakpoint 1
func ConvertToolsToClaude(tools []v1.ToolDefinition, enableCacheBreakpoint bool) []ClaudeTool {
	out := make([]ClaudeTool, 0, len(tools))
	for _, t := range tools {
		var schema any
		if len(t.Parameters) > 0 {
			_ = json.Unmarshal(t.Parameters, &schema)
		}
		if schema == nil {
			schema = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		out = append(out, ClaudeTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: CanonicalizeValue(schema),
		})
	}
	if enableCacheBreakpoint && len(out) > 0 {
		out[len(out)-1].CacheControl = &ClaudeCacheCtrl{Type: "ephemeral"}
	}
	return out
}

// InjectClaudeMessageCacheBreakpoint 在倒数第 2 轮历史 User 消息的末尾块挂载 Breakpoint 2
func InjectClaudeMessageCacheBreakpoint(msgs []ClaudeMessage) {
	userCount := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			userCount++
			if userCount == 2 {
				if len(msgs[i].Content) > 0 {
					msgs[i].Content[len(msgs[i].Content)-1].CacheControl = &ClaudeCacheCtrl{Type: "ephemeral"}
				}
				break
			}
		}
	}
}

// ConvertCanonicalToClaude 将中立消息列表转换为 Claude 标准请求体
// 返回：提取出的顶级 systemPrompt, 严格 user/assistant 消息数组, 错误
func ConvertCanonicalToClaude(messages []CanonicalMessage) (systemPrompt string, out []ClaudeMessage, err error) {
	out = make([]ClaudeMessage, 0, len(messages))

	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			// 提取所有 system 角色内容合并至顶层
			for _, p := range msg.Parts {
				if p.Type == ContentText {
					if systemPrompt != "" {
						systemPrompt += "\n\n"
					}
					systemPrompt += p.Text
				}
			}

		case RoleUser:
			blocks := make([]ClaudeBlock, 0, len(msg.Parts))
			for _, p := range msg.Parts {
				if p.Type == ContentText {
					block := ClaudeBlock{Type: "text", Text: p.Text}
					if p.CacheMarker {
						block.CacheControl = &ClaudeCacheCtrl{Type: "ephemeral"}
					}
					blocks = append(blocks, block)
				}
			}
			if len(blocks) > 0 {
				out = append(out, ClaudeMessage{Role: "user", Content: blocks})
			}

		case RoleAssistant:
			blocks := make([]ClaudeBlock, 0, len(msg.Parts))
			for _, p := range msg.Parts {
				switch p.Type {
				case ContentText:
					blocks = append(blocks, ClaudeBlock{Type: "text", Text: p.Text})
				case ContentThinking:
					blocks = append(blocks, ClaudeBlock{Type: "thinking", Thinking: p.Thinking})
				case ContentToolUse:
					if p.ToolCall != nil {
						blocks = append(blocks, ClaudeBlock{
							Type:  "tool_use",
							ID:    p.ToolCall.ID,
							Name:  p.ToolCall.Name,
							Input: p.ToolCall.Arguments,
						})
					}
				}
			}
			if len(blocks) > 0 {
				out = append(out, ClaudeMessage{Role: "assistant", Content: blocks})
			}

		case RoleTool:
			// Claude 规范：tool_result 必须作为 user 消息中的块传入
			blocks := make([]ClaudeBlock, 0, len(msg.Parts))
			for _, p := range msg.Parts {
				if p.Type == ContentToolResult && p.ToolResult != nil {
					blocks = append(blocks, ClaudeBlock{
						Type:      "tool_result",
						ToolUseID: p.ToolResult.ToolCallID,
						Content:   p.ToolResult.Content,
						IsError:   p.ToolResult.IsError,
					})
				}
			}
			if len(blocks) > 0 {
				out = append(out, ClaudeMessage{Role: "user", Content: blocks})
			}

		default:
			return "", nil, fmt.Errorf("unsupported canonical role for Claude: %s", msg.Role)
		}
	}

	return systemPrompt, out, nil
}
