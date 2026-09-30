package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/channels"
)

// 0.0.11：渠道级重试（429/5xx/连接失败）之间必须有退避——立刻换渠道连打只会
// 把限流窗口拖长、对上游不友好。断言取下界（慢机器照常通过）；退避表注入短值。
func TestGateway_ChannelRetryBacksOff(t *testing.T) {
	old := llm.RetryBackoff
	llm.RetryBackoff = []time.Duration{80 * time.Millisecond, 120 * time.Millisecond}
	defer func() { llm.RetryBackoff = old }()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests) // 渠道级故障（429）→ 换渠道重试
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseOK))
	}))
	t.Cleanup(srv.Close)

	g := newGateway(t,
		chanOf("a", "m", 100, func(c *channels.Channel) { c.BaseURL = srv.URL }),
		chanOf("b", "m", 90, func(c *channels.Channel) { c.BaseURL = srv.URL }),
	)
	start := time.Now()
	ch, err := g.StreamChat(context.Background(), llm.ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	text, terminal := collect(t, ch)
	if terminal.EndReason != llm.EndDone {
		t.Fatalf("换渠道重试后应成功：%q / %+v", text, terminal)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("calls = %d, want 2（429 后换渠道重试一次）", n)
	}
	// 80ms 的注入退避：取下界 60ms（只在方向明确处断言，避免时序假红）
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Fatalf("渠道重试之间必须有退避：实际仅 %v", elapsed)
	}
}
