package openaiprovider

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// 2026-10-19：最后一段内容与 finish_reason 同在一个分片时，内容必须先发出再给终态
// （此前先判终态直接 return，答复缺尾、最后一个工具调用分片丢失）。
func TestProviderStream_ContentInFinishChunkNotDropped(t *testing.T) {
	srv := httptest.NewServer(sseHandler(
		deltaLine("前半"),
		`{"choices":[{"delta":{"content":"后半","tool_calls":[{"index":0,"id":"c1","function":{"name":"fs","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		"[DONE]",
	))
	defer srv.Close()
	ch, err := newTestProvider(t, srv.URL, time.Second).StreamChat(context.Background(), llm.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	calls, terminals := 0, 0
	for _, c := range collect(t, ch, 3*time.Second) {
		if c.EndReason != llm.EndNone {
			terminals++
			continue
		}
		if terminals > 0 {
			t.Fatal("终态之后不应再有内容块")
		}
		text.WriteString(c.Delta)
		calls += len(c.ToolCalls)
	}
	if text.String() != "前半后半" || calls != 1 || terminals != 1 {
		t.Fatalf("finish 分片里的内容丢了：text=%q calls=%d terminals=%d", text.String(), calls, terminals)
	}
}
