package openaiprovider

import (
	"encoding/json"
	"strings"
	"testing"

	"tiancode/internal/core/llm"
)

// 0.0.10：多模态请求形态契约——纯文本保持字符串 content；带图/内联文件时
// content 用数组（先 text，再 image_url[data URL, detail=auto]）。
func partsFrom(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	msgs := got["messages"].([]any)
	m0 := msgs[0].(map[string]any)
	switch c := m0["content"].(type) {
	case string:
		return nil
	case []any:
		out := make([]map[string]any, 0, len(c))
		for _, p := range c {
			out = append(out, p.(map[string]any))
		}
		return out
	default:
		t.Fatalf("content 类型 = %T", c)
		return nil
	}
}

// 只有文字：content 仍是字符串（不把纯文本改成数组）。
func TestMarshal_TextOnlyContentIsString(t *testing.T) {
	body, err := MarshalChatRequest(llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	m0 := got["messages"].([]any)[0].(map[string]any)
	if _, ok := m0["content"].(string); !ok {
		t.Fatalf("纯文本 content 必须保持字符串：%T", m0["content"])
	}
}

// 文字 + 一张图：content 数组 [text, image_url(data URL, detail=auto)]。
func TestMarshal_TextPlusImage(t *testing.T) {
	msg := llm.Message{
		Role: "user", Content: "看这张图",
		Parts: []llm.ContentPart{
			{Type: "text", Text: "看这张图"},
			{Type: "image_url", ImageURL: "data:image/png;base64,AAAA"},
		},
	}
	body, err := MarshalChatRequest(llm.ChatRequest{Messages: []llm.Message{msg}})
	if err != nil {
		t.Fatal(err)
	}
	parts := partsFrom(t, body)
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	if parts[0]["type"] != "text" || parts[0]["text"] != "看这张图" {
		t.Fatalf("text part = %v", parts[0])
	}
	img := parts[1]["image_url"].(map[string]any)
	if img["url"] != "data:image/png;base64,AAAA" {
		t.Fatalf("image url = %v", img["url"])
	}
	if img["detail"] != "auto" {
		t.Fatalf("detail = %v, want auto", img["detail"])
	}
}

// 只有一张图（无文字）：数组里只有 image_url（空 text 省略）。
func TestMarshal_ImageOnly(t *testing.T) {
	msg := llm.Message{
		Role: "user",
		Parts: []llm.ContentPart{
			{Type: "image_url", ImageURL: "data:image/jpeg;base64,BBBB"},
		},
	}
	body, err := MarshalChatRequest(llm.ChatRequest{Messages: []llm.Message{msg}})
	if err != nil {
		t.Fatal(err)
	}
	parts := partsFrom(t, body)
	if len(parts) != 1 || parts[0]["type"] != "image_url" {
		t.Fatalf("应只有 image part：%v", parts)
	}
}

// 文字 + 内联文本文件：内容放进 text part（不伪装成图片）。
func TestMarshal_TextPlusInlineFile(t *testing.T) {
	msg := llm.Message{
		Role: "user", Content: "这份日志",
		Parts: []llm.ContentPart{
			{Type: "text", Text: "这份日志\n\n[附件文件 logs/app.log 内容如下]\nERROR line"},
		},
	}
	body, err := MarshalChatRequest(llm.ChatRequest{Messages: []llm.Message{msg}})
	if err != nil {
		t.Fatal(err)
	}
	parts := partsFrom(t, body)
	if len(parts) != 1 || parts[0]["type"] != "text" {
		t.Fatalf("应只有 text part：%v", parts)
	}
	if !strings.Contains(parts[0]["text"].(string), "ERROR line") {
		t.Fatalf("内联内容缺失：%v", parts[0]["text"])
	}
}
