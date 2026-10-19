package adaptors

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

func anthropicTerminal(t *testing.T, stopReason string) llm.StreamChunk {
	t.Helper()
	sse := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"写到一半"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"` + stopReason + `"},"usage":{"output_tokens":8192}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
	srv := httptest.NewServer(http200(sse))
	defer srv.Close()
	a := Anthropic{IdleTimeout: 2 * time.Second}
	rc := RouteContext{BaseURL: srv.URL, Credential: "sk-ant"}
	resp, err := a.DoRequest(context.Background(), rc, mapHeader(), []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	ch, err := a.ConvertResponse(context.Background(), rc, resp)
	if err != nil {
		t.Fatal(err)
	}
	var term llm.StreamChunk
	for c := range ch {
		if c.EndReason != llm.EndNone {
			term = c
		}
	}
	return term
}

// P1：stop_reason=max_tokens 必须在 EndDone 终态上标 Truncated（此前与正常写完无法区分）。
func TestAnthropic_MaxTokensMarksTruncated(t *testing.T) {
	if term := anthropicTerminal(t, "max_tokens"); term.EndReason != llm.EndDone || !term.Truncated {
		t.Fatalf("max_tokens 应为 EndDone+Truncated：%+v", term)
	}
	if term := anthropicTerminal(t, "end_turn"); term.EndReason != llm.EndDone || term.Truncated {
		t.Fatalf("end_turn 不应标截断：%+v", term)
	}
}
