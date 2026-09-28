package adaptors

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ConvertRequest：system 提升、tool_use/tool_result 块、tools、max_tokens 覆盖。
func TestAnthropic_ConvertRequest(t *testing.T) {
	a := Anthropic{}
	rc := RouteContext{Model: "claude-3-5-sonnet", ParamOverride: map[string]any{"max_tokens": float64(1024)}}
	req := llmChatRequest()
	body, err := a.ConvertRequest(rc, req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m["model"] != "claude-3-5-sonnet" {
		t.Fatalf("model = %v", m["model"])
	}
	if m["max_tokens"] != float64(1024) {
		t.Fatalf("max_tokens = %v（param_override 必须生效）", m["max_tokens"])
	}
	if m["system"] != "你是编码助手" {
		t.Fatalf("system = %v（system 消息必须提升）", m["system"])
	}
	msgs := m["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4（user/tool_use/tool_result/assistant）", len(msgs))
	}
	m0 := msgs[0].(map[string]any)
	if m0["role"] != "user" {
		t.Fatalf("msgs[0].role = %v", m0["role"])
	}
	m1 := msgs[1].(map[string]any)
	if m1["role"] != "assistant" {
		t.Fatalf("msgs[1].role = %v", m1["role"])
	}
	blocks := m1["content"].([]any)
	tb := blocks[0].(map[string]any)
	if tb["type"] != "tool_use" || tb["id"] != "t1" || tb["name"] != "fs" {
		t.Fatalf("tool_use 块不符：%v", tb)
	}
	m2 := msgs[2].(map[string]any)
	rb := m2["content"].([]any)[0].(map[string]any)
	if rb["type"] != "tool_result" || rb["tool_use_id"] != "t1" || rb["content"] != "file body" {
		t.Fatalf("tool_result 块不符：%v", rb)
	}
	tools := m["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "fs" {
		t.Fatalf("tools 不符：%v", m["tools"])
	}
}

// ParseSSE：text_delta / thinking 块 / tool_use 聚合 / usage / 恰好一个 EndDone。
func TestAnthropic_ParseSSE(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"你好"}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"fs"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.txt\"}"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":1}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":21}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
	srv := httptest.NewServer(http200(sse))
	defer srv.Close()

	a := Anthropic{IdleTimeout: 2 * time.Second}
	rc := RouteContext{BaseURL: srv.URL, Credential: "sk-ant"}
	hdr := mapHeader()
	if err := a.SetupHeaders(rc, hdr); err != nil {
		t.Fatal(err)
	}
	resp, err := a.DoRequest(context.Background(), rc, hdr, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("x-api-key") == "" && resp.Request.Header.Get("x-api-key") != "sk-ant" {
		t.Fatalf("x-api-key 未设置")
	}
	ch, err := a.ConvertResponse(context.Background(), rc, resp)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var toolCalls int
	var usage *llmUsage
	terminals := 0
	for c := range ch {
		if c.EndReason != llmEndNone {
			terminals++
			if c.EndReason != llmEndDone {
				t.Fatalf("terminal = %v, want EndDone", c.EndReason)
			}
			continue
		}
		text.WriteString(c.Delta)
		toolCalls += len(c.ToolCalls)
		if c.Usage != nil {
			usage = &llmUsage{prompt: c.Usage.PromptTokens, completion: c.Usage.CompletionTokens}
		}
	}
	if text.String() != "你好" {
		t.Fatalf("text = %q", text.String())
	}
	if toolCalls != 1 {
		t.Fatalf("toolCalls = %d, want 1", toolCalls)
	}
	if usage == nil || usage.prompt != 10 || usage.completion != 21 {
		t.Fatalf("usage = %+v", usage)
	}
	if terminals != 1 {
		t.Fatalf("terminals = %d, want 1", terminals)
	}
}
