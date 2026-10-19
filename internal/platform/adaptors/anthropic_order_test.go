package adaptors

import (
	"encoding/json"
	"testing"

	"tiancode/internal/core/llm"
)

// 内核产出的"工具结果 + 回合内截图"序列转成 Anthropic 后，tool_result 块必须排在
// user 消息内容的最前（API 要求），截图块在其后。
func TestAnthropic_ToolResultBeforeScreenshot(t *testing.T) {
	note := "（系统）截图如下"
	req := llm.ChatRequest{Messages: []llm.Message{
		{Role: "user", Content: "看页面"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "t1", Name: "browser", Arguments: `{}`}}},
		{Role: "tool", ToolCallID: "t1", Content: "已截图"},
		{Role: "user", Content: note, Parts: []llm.ContentPart{
			{Type: "text", Text: note},
			{Type: "image_url", ImageURL: "data:image/png;base64,AAAA"},
		}},
	}}
	body, err := Anthropic{}.ConvertRequest(RouteContext{Model: "m"}, req)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	last := m.Messages[len(m.Messages)-1]
	if last.Role != "user" || len(last.Content) < 2 || last.Content[0].Type != "tool_result" {
		t.Fatalf("tool_result 必须是 user 消息的第一个块：%+v", m.Messages)
	}
	hasImage := false
	for _, b := range last.Content[1:] {
		if b.Type == "image" {
			hasImage = true
		}
	}
	if !hasImage {
		t.Fatalf("截图块应跟在 tool_result 之后：%+v", last.Content)
	}
}
