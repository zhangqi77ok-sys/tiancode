package openaiprovider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// collect 读取流直至关闭或超时，返回收到的全部块。
func collect(t *testing.T, ch <-chan llm.StreamChunk, timeout time.Duration) []llm.StreamChunk {
	t.Helper()
	var out []llm.StreamChunk
	deadline := time.After(timeout)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, c)
		case <-deadline:
			t.Fatalf("stream did not close within %v; got %d chunks", timeout, len(out))
		}
	}
}

// sseHandler 返回按顺序写入 data 行的处理器；每行自带 SSE 空行分隔。
func sseHandler(lines ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ln := range lines {
			fmt.Fprintf(w, "data: %s\n\n", ln)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func deltaLine(content string) string {
	return fmt.Sprintf(`{"choices":[{"delta":{"content":%q}}]}`, content)
}

func newTestProvider(t *testing.T, url string, idle time.Duration) *Provider {
	t.Helper()
	return New(Options{
		BaseURL:     url,
		APIKey:      "test-key",
		IdleTimeout: idle,
		HTTPClient:  &http.Client{Timeout: 5 * time.Second},
	})
}

// C-LLM-1：上游正常完成（finish_reason=stop + [DONE]）→ 增量块 + 恰好一个 EndDone 终态，随后关闭。
func TestProviderStream_DoneTerminals(t *testing.T) {
	srv := httptest.NewServer(sseHandler(
		deltaLine("he"),
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		"[DONE]",
	))
	defer srv.Close()

	ch, err := newTestProvider(t, srv.URL, time.Second).StreamChat(context.Background(), llm.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	chunks := collect(t, ch, 3*time.Second)

	var terminals int
	var deltas int
	for _, c := range chunks {
		if c.EndReason != llm.EndNone {
			terminals++
			if c.EndReason != llm.EndDone {
				t.Fatalf("terminal = %v, want EndDone", c.EndReason)
			}
		} else if c.Delta != "" {
			deltas++
		}
	}
	if deltas == 0 {
		t.Fatal("no delta chunks received")
	}
	if terminals != 1 {
		t.Fatalf("terminal count = %d, want 1", terminals)
	}
}

// C-LLM-2：上游挂起（收到头后不出数据）→ 空闲看门狗触发 EndIdleTimeout，不永久阻塞。
func TestProviderStream_IdleTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()

	ch, err := newTestProvider(t, srv.URL, 80*time.Millisecond).StreamChat(context.Background(), llm.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	start := time.Now()
	chunks := collect(t, ch, 3*time.Second)
	if time.Since(start) > 2*time.Second {
		t.Fatal("idle timeout took too long")
	}
	terminals := 0
	for _, c := range chunks {
		if c.EndReason != llm.EndNone {
			terminals++
			if c.EndReason != llm.EndIdleTimeout {
				t.Fatalf("terminal = %v, want EndIdleTimeout", c.EndReason)
			}
		}
	}
	if terminals != 1 {
		t.Fatalf("terminal count = %d, want 1 (EndIdleTimeout)", terminals)
	}
}

// C-LLM-3：ctx 取消 → EndCancelled 终态收束；消费方读到关闭为止。
func TestProviderStream_Cancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+deltaLine("first")+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := newTestProvider(t, srv.URL, 5*time.Second).StreamChat(ctx, llm.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var gotCancel bool
	gotFirst := false
	deadline := time.After(3 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				if !gotCancel {
					t.Fatal("stream closed without EndCancelled terminal")
				}
				return
			}
			if !gotFirst {
				// 读到首块后取消，模拟用户点"中断"
				gotFirst = true
				cancel()
			}
			if c.EndReason == llm.EndCancelled {
				gotCancel = true
			}
		case <-deadline:
			t.Fatal("stream did not close after cancel")
		}
	}
}

// C-LLM-4：流内上游错误报文 → EndError 终态且 Err 携带原因（fail-closed）。
func TestProviderStream_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(sseHandler(
		`{"error":{"message":"quota exceeded","type":"insufficient_quota"}}`,
	))
	defer srv.Close()

	ch, err := newTestProvider(t, srv.URL, time.Second).StreamChat(context.Background(), llm.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	chunks := collect(t, ch, 3*time.Second)
	for _, c := range chunks {
		if c.EndReason == llm.EndError {
			if c.Err == nil || !strings.Contains(c.Err.Error(), "quota exceeded") {
				t.Fatalf("EndError.Err = %v, want contains 'quota exceeded'", c.Err)
			}
			return
		}
	}
	t.Fatalf("no EndError terminal in %d chunks", len(chunks))
}

// C-LLM-5：连接中断（服务端中途断开）→ EndError 终态，不静默结束。
func TestProviderStream_ConnReset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+deltaLine("partial")+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// 劫持连接后粗暴断开，制造 scanner 错误
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("hijack unsupported")
		}
		conn, buf, _ := hj.Hijack()
		buf.Flush()
		conn.Close()
	}))
	defer srv.Close()

	ch, err := newTestProvider(t, srv.URL, time.Second).StreamChat(context.Background(), llm.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	chunks := collect(t, ch, 3*time.Second)
	for _, c := range chunks {
		if c.EndReason == llm.EndError {
			if c.Err == nil {
				t.Fatal("EndError.Err is nil")
			}
			return
		}
	}
	t.Fatal("no EndError terminal after connection reset")
}

// C-LLM-6：消费方停止读取并取消 → 生产方经发送逃生退出并关闭 channel（无泄漏）。
func TestProviderStream_SlowConsumerEscape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// 持续吐数据，制造消费方跟不上积压
		for i := 0; i < 5000; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			fmt.Fprint(w, "data: "+deltaLine(fmt.Sprint(i))+"\n\n")
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := newTestProvider(t, srv.URL, 5*time.Second).StreamChat(ctx, llm.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	// 不读取任何块，直接取消
	cancel()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // 已关闭：生产方退出
			}
		case <-deadline:
			t.Fatal("producer did not exit after cancel (goroutine/connection leak)")
		}
	}
}

// C-LLM-7：整流 EndReason != EndNone 的块恰好 1 个。
func TestProviderStream_SingleTerminal(t *testing.T) {
	srv := httptest.NewServer(sseHandler(
		deltaLine("a"), deltaLine("b"),
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		"[DONE]",
	))
	defer srv.Close()

	ch, err := newTestProvider(t, srv.URL, time.Second).StreamChat(context.Background(), llm.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	chunks := collect(t, ch, 3*time.Second)
	terminals := 0
	for _, c := range chunks {
		if c.EndReason != llm.EndNone {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("terminal count = %d, want 1", terminals)
	}
}

// 前置条件：HTTP 非 2xx 必须以 error 返回（流尚未开始）——这是 C-RT-1 流前重试的前提。
func TestProviderStream_HTTPErrorReturnsPreStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := newTestProvider(t, srv.URL, time.Second).StreamChat(context.Background(), llm.ChatRequest{Model: "m"})
	if err == nil {
		t.Fatal("HTTP 401 must return pre-stream error, not a channel")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v, want contains HTTP status", err)
	}
}
