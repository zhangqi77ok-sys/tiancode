package openaiprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// 回归锁定：工具名必须从 **function.name** 解析（标准 OpenAI 流式形态）。
//
// 事故：曾按顶层 `name` 解析 → 工具名恒为空字符串 → agent 回填 assistant.tool_calls
// 时缺 id/name → 上游 HTTP 400 "assistant.tool_calls 缺少有效 id 或 name"。
// 旧单测之所以没抓到：模拟报文的形状跟真实网关不一致（这正是需要真链路测试的原因）。
func TestStreamChat_ParsesToolNameFromFunctionObject(t *testing.T) {
	// 真实形态：分片先给 id/name，后续分片只追加 arguments（且 arguments 被切碎）
	const body = "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_abc\",\"type\":\"function\",\"function\":{\"name\":\"fs\",\"arguments\":\"{\\\"action\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"read\\\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p := New(Options{BaseURL: srv.URL, HTTPClient: &http.Client{Timeout: 10 * time.Second}})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ch, err := p.StreamChat(ctx, llm.ChatRequest{
		Model:    "m",
		Messages: []llm.Message{{Role: "user", Content: "读文件"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var got []llm.ToolCallChunk
	for c := range ch {
		got = append(got, c.ToolCalls...)
	}
	if len(got) == 0 {
		t.Fatal("未解析出任何工具调用分片")
	}
	if got[0].ID != "call_abc" {
		t.Fatalf("首片 ID = %q, want call_abc", got[0].ID)
	}
	if got[0].Name != "fs" {
		t.Fatalf("首片 Name = %q, want fs（名字在 function.name 里，非顶层 name）", got[0].Name)
	}
	// 参数分片必须完整拼接后可解析
	args := ""
	for _, c := range got {
		args += c.ArgumentsDelta
	}
	if args != `{"action":"read"}` {
		t.Fatalf("拼接后的参数 = %q", args)
	}
}
