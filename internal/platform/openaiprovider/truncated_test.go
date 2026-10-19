package openaiprovider

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

func terminalFor(t *testing.T, finish string) llm.StreamChunk {
	t.Helper()
	srv := httptest.NewServer(sseHandler(
		deltaLine("写到一半"),
		`{"choices":[{"delta":{},"finish_reason":"`+finish+`"}]}`,
		"[DONE]",
	))
	defer srv.Close()
	ch, err := newTestProvider(t, srv.URL, time.Second).StreamChat(context.Background(), llm.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var term llm.StreamChunk
	for _, c := range collect(t, ch, 3*time.Second) {
		if c.EndReason != llm.EndNone {
			term = c
		}
	}
	return term
}

// P1：finish_reason=length 必须在 EndDone 终态上标 Truncated。
func TestProviderStream_LengthMarksTruncated(t *testing.T) {
	if term := terminalFor(t, "length"); term.EndReason != llm.EndDone || !term.Truncated {
		t.Fatalf("length 应为 EndDone+Truncated：%+v", term)
	}
	if term := terminalFor(t, "stop"); term.EndReason != llm.EndDone || term.Truncated {
		t.Fatalf("stop 不应标截断：%+v", term)
	}
}
