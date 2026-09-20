package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "tiancode/pkg/plugin/v1"
)

func TestAnthropicProvider_StreamChatPromptCaching(t *testing.T) {
	// 启动 mock SSE 服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 校验头部包含 anthropic-beta: prompt-caching-2024-07-31
		betaHeader := r.Header.Get("anthropic-beta")
		if betaHeader != "prompt-caching-2024-07-31" {
			t.Errorf("Expected anthropic-beta header 'prompt-caching-2024-07-31', got: %s", betaHeader)
		}

		// 校验请求体中的 tools 是否在最后一个挂载了 cache_control
		var reqPayload struct {
			Tools []struct {
				Name         string `json:"name"`
				CacheControl *struct {
					Type string `json:"type"`
				} `json:"cache_control"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
			t.Fatalf("Failed to decode request body: %v", err)
		}
		if len(reqPayload.Tools) != 2 {
			t.Errorf("Expected 2 tools, got %d", len(reqPayload.Tools))
		} else if reqPayload.Tools[1].CacheControl == nil || reqPayload.Tools[1].CacheControl.Type != "ephemeral" {
			t.Errorf("Last tool must have cache_control 'ephemeral'")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.ResponseWriter to be a Flusher")
		}

		// 发送 message_start 带 cache_read_input_tokens
		fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5000,\"cache_read_input_tokens\":4200,\"cache_creation_input_tokens\":800}}}\n\n")
		flusher.Flush()

		// 发送 content_block_delta
		fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello cached!\"}}\n\n")
		flusher.Flush()

		// 发送 message_delta 带 output_tokens
		fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":20}}\n\n")
		flusher.Flush()

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	prov := NewProvider()
	cfg, _ := json.Marshal(map[string]string{
		"api_key":  "test-api-key",
		"base_url": server.URL,
	})
	if err := prov.Init(context.Background(), cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	msgs, _ := json.Marshal([]map[string]any{
		{"role": "user", "content": "hello"},
	})
	tools := []v1.ToolDefinition{
		{Name: "tool_a", Description: "desc a", Parameters: json.RawMessage(`{}`)},
		{Name: "tool_b", Description: "desc b", Parameters: json.RawMessage(`{}`)},
	}

	ch, err := prov.StreamChat(context.Background(), &v1.ChatRequest{
		Model:    "claude-3-7-sonnet-20250219",
		Messages: msgs,
		Tools:    tools,
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("StreamChat failed: %v", err)
	}

	var totalDelta string
	var finalUsage *v1.TokenUsage

	for chunk := range ch {
		if chunk.Error != nil {
			t.Fatalf("Stream chunk error: %v", chunk.Error)
		}
		if chunk.DeltaContent != "" {
			totalDelta += chunk.DeltaContent
		}
		if chunk.Usage != nil {
			finalUsage = chunk.Usage
		}
	}

	if totalDelta != "Hello cached!" {
		t.Errorf("Expected 'Hello cached!', got: %s", totalDelta)
	}

	if finalUsage == nil {
		t.Fatal("Expected final usage, got nil")
	}

	if finalUsage.CacheReadTokens != 4200 {
		t.Errorf("Expected 4200 CacheReadTokens, got %d", finalUsage.CacheReadTokens)
	}
	if finalUsage.CacheCreationTokens != 800 {
		t.Errorf("Expected 800 CacheCreationTokens, got %d", finalUsage.CacheCreationTokens)
	}
	if finalUsage.PromptTokens != 5000 {
		t.Errorf("Expected 5000 PromptTokens, got %d", finalUsage.PromptTokens)
	}
	if finalUsage.CompletionTokens != 20 {
		t.Errorf("Expected 20 CompletionTokens, got %d", finalUsage.CompletionTokens)
	}
}
