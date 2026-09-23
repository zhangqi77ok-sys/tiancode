package openaiprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// 请求转换：ChatRequest.Tools 必须以 OpenAI wire 形态出现在请求体中
//（new-api 式 Convert 边界：中性结构 → 厂商私有格式）。
func TestProviderStream_SendsToolsInRequest(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	params := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	ch, err := newTestProvider(t, srv.URL, time.Second).StreamChat(context.Background(), llm.ChatRequest{
		Model: "m",
		Tools: []llm.ToolDef{
			{Name: "fs", Description: "filesystem ops", Parameters: params},
		},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	for range ch {
	}

	tools, ok := gotBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("request tools = %#v, want 1 entry", gotBody["tools"])
	}
	first, _ := tools[0].(map[string]any)
	if first["type"] != "function" {
		t.Fatalf("tool type = %v, want function", first["type"])
	}
	fn, _ := first["function"].(map[string]any)
	if fn["name"] != "fs" || fn["description"] != "filesystem ops" {
		t.Fatalf("tool function = %#v", fn)
	}
	if _, ok := fn["parameters"]; !ok {
		t.Fatal("tool parameters missing")
	}
}
