package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	v1 "tiancode/pkg/plugin/v1"
)

func TestOpenAIProvider_ReasoningAliasAndBuffer(t *testing.T) {
	// 模拟返回携带 reasoning 别名字段的 SSE 流
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// 包含 reasoning 别名字段
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"思考过程A\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"正文内容B\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := NewProvider()
	p.baseURL = server.URL
	p.apiKey = "test-api-key"

	req := &v1.ChatRequest{
		Model:  "deepseek-reasoner",
		Stream: true,
	}

	ch, err := p.StreamChat(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamChat failed: %v", err)
	}

	gotThinking := false
	gotContent := false

	for chunk := range ch {
		if chunk.Thinking == "思考过程A" {
			gotThinking = true
		}
		if chunk.DeltaContent == "正文内容B" {
			gotContent = true
		}
	}

	if !gotThinking {
		t.Errorf("expected thinking '思考过程A', but not captured from reasoning field")
	}
	if !gotContent {
		t.Errorf("expected content '正文内容B', but not received")
	}
}

func TestOpenAIProvider_ListModels_FailClosedWithoutKey(t *testing.T) {
	p := NewProvider()
	p.apiKey = ""
	p.baseURL = "https://api.openai.com/v1"
	_, err := p.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error without API key")
	}
}

func TestOpenAIProvider_ListModels_FromUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"local-only-model"}]}`))
	}))
	defer server.Close()
	p := NewProvider()
	p.baseURL = server.URL
	p.apiKey = "fake-api-key-0123456789abcdef"
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "local-only-model" {
		t.Fatalf("got %+v", models)
	}
}

func TestOpenAIProvider_UpstreamErrorInSSE(t *testing.T) {
	// 模拟返回包含 error 报文的 SSE 流
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// 模拟上游速率限制或网关报错
		_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"Rate limit exceeded: please slow down\",\"type\":\"insufficient_quota\"}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := NewProvider()
	p.baseURL = server.URL
	p.apiKey = "test-api-key"

	req := &v1.ChatRequest{
		Model:  "deepseek-chat",
		Stream: true,
	}

	ch, err := p.StreamChat(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamChat failed: %v", err)
	}

	gotError := false
	for chunk := range ch {
		if chunk.Error != nil {
			gotError = true
			if chunk.Error.Error() != "upstream API error: Rate limit exceeded: please slow down" {
				t.Errorf("unexpected error message: %v", chunk.Error)
			}
		}
	}

	if !gotError {
		t.Errorf("expected error chunk from SSE error payload, but got none")
	}
}

func TestOpenAIProvider_PromptCachingUsage(t *testing.T) {
	// 模拟 DeepSeek/OpenAI 返回 cached_tokens
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Answer\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":8000,\"completion_tokens\":50,\"total_tokens\":8050,\"prompt_cache_hit_tokens\":7200,\"prompt_tokens_details\":{\"cached_tokens\":7200}}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := NewProvider()
	p.baseURL = server.URL
	p.apiKey = "test-api-key"

	req := &v1.ChatRequest{
		Model:  "deepseek-chat",
		Stream: true,
	}

	ch, err := p.StreamChat(context.Background(), req)
	if err != nil {
		t.Fatalf("StreamChat failed: %v", err)
	}

	var finalUsage *v1.TokenUsage
	for chunk := range ch {
		if chunk.Usage != nil {
			finalUsage = chunk.Usage
		}
	}

	if finalUsage == nil {
		t.Fatal("Expected usage chunk, got nil")
	}
	if finalUsage.CacheReadTokens != 7200 {
		t.Errorf("Expected 7200 CacheReadTokens, got %d", finalUsage.CacheReadTokens)
	}
	if finalUsage.PromptTokens != 8000 {
		t.Errorf("Expected 8000 PromptTokens, got %d", finalUsage.PromptTokens)
	}
}


