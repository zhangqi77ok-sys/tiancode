package adaptors

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// 2026-10-19：带签名的思考块整块交给内核；cache 读写 token 进 Usage。
func TestAnthropic_ParsesSignedThinkingAndCacheUsage(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":900,"cache_creation_input_tokens":50}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"先读文件"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","delta":{"type":"signature_delta","signature":"SIG123"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"ENC"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":1}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		"",
	}, "\n")
	srv := httptest.NewServer(http200(sse))
	defer srv.Close()
	a := Anthropic{IdleTimeout: 2 * time.Second}
	rc := RouteContext{BaseURL: srv.URL, Credential: "k"}
	resp, err := a.DoRequest(context.Background(), rc, mapHeader(), []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	ch, err := a.ConvertResponse(context.Background(), rc, resp)
	if err != nil {
		t.Fatal(err)
	}
	var blocks []llm.ThinkingBlock
	var usage *llm.Usage
	for c := range ch {
		if c.ThinkingBlock != nil {
			blocks = append(blocks, *c.ThinkingBlock)
		}
		if c.Usage != nil {
			usage = c.Usage
		}
	}
	if len(blocks) != 2 || blocks[0].Thinking != "先读文件" || blocks[0].Signature != "SIG123" || blocks[1].Redacted != "ENC" {
		t.Fatalf("思考块解析不符：%+v", blocks)
	}
	if usage == nil || usage.CacheReadTokens != 900 || usage.CacheWriteTokens != 50 {
		t.Fatalf("cache 用量应解析：%+v", usage)
	}
}

// 助手消息的思考块按上游要求排在最前回传；没有签名的思考文本不回传。
func TestAnthropic_ConvertRequestSendsThinkingFirst(t *testing.T) {
	req := llm.ChatRequest{Messages: []llm.Message{
		{Role: "user", Content: "改一下"},
		{Role: "assistant", Content: "我先读", ToolCalls: []llm.ToolCall{{ID: "t1", Name: "fs", Arguments: `{}`}},
			ThinkingBlocks: []llm.ThinkingBlock{{Thinking: "想", Signature: "S"}, {Thinking: "无签名"}, {Redacted: "R"}}},
		{Role: "tool", ToolCallID: "t1", Content: "x"},
	}}
	body, err := Anthropic{}.ConvertRequest(RouteContext{Model: "m"}, req)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	as := m.Messages[1].Content
	if len(as) != 4 || as[0]["type"] != "thinking" || as[0]["signature"] != "S" || as[1]["type"] != "redacted_thinking" || as[1]["data"] != "R" || as[2]["type"] != "text" || as[3]["type"] != "tool_use" {
		t.Fatalf("助手消息块顺序应为 thinking/redacted/text/tool_use：%+v", as)
	}
}
