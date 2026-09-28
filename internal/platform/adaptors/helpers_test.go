package adaptors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"tiancode/internal/core/llm"
)

// 测试助手：统一夹具（避免测试文件之间重复声明冲突）。

type llmUsage struct{ prompt, completion int64 }

const llmEndNone = llm.EndNone
const llmEndDone = llm.EndDone

// llmChatRequest 覆盖 system/user/assistant(tool_use)/tool/assistant 的典型请求。
func llmChatRequest() llm.ChatRequest {
	return llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "你是编码助手"},
			{Role: "user", Content: "读文件"},
			{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "t1", Name: "fs", Arguments: `{"path":"a.txt"}`}}},
			{Role: "tool", ToolCallID: "t1", Content: "file body"},
			{Role: "assistant", Content: "读完了"},
		},
		Tools: []llm.ToolDef{{Name: "fs", Description: "文件", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
}

// http200 返回固定 SSE 正文的测试上游。
func http200(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}
}

// mapHeader 构造空请求头。
func mapHeader() http.Header { return http.Header{} }

// 防 httptest/context 未用导入（真实使用在测试体内）。
var (
	_ = httptest.NewServer
	_ = context.Background
)
