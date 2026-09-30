package llm

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 第 3 批：超时分层（等待首字节 / 流式全程）与终态文案。

// silentProvider：建流成功但永不产块（思考模型首字节前的静默）。
type silentProvider struct{ calls int }

func (s *silentProvider) StreamChat(ctx context.Context, _ ChatRequest) (<-chan StreamChunk, error) {
	s.calls++
	ch := make(chan StreamChunk)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

// slowStreamProvider：立刻给首块，随后静默一段时间才正常结束（慢流）。
type slowStreamProvider struct{}

func (slowStreamProvider) StreamChat(ctx context.Context, _ ChatRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk)
	go func() {
		defer close(ch)
		ch <- StreamChunk{Delta: "first"}
		select {
		case <-time.After(250 * time.Millisecond):
			ch <- StreamChunk{EndReason: EndDone}
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

func drainTerminal(t *testing.T, ch <-chan StreamChunk) StreamChunk {
	t.Helper()
	var terminal StreamChunk
	for c := range ch {
		if c.EndReason != EndNone {
			terminal = c
		}
	}
	return terminal
}

// 首字节预算用尽 → EndError 且文案写明"首个数据"（可重试/可调预算，不是上游错误）。
func TestRuntime_FirstByteBudgetExpires(t *testing.T) {
	sp := &silentProvider{}
	rt := NewChatRuntime(sp, TimeoutBudget{FirstByte: 100 * time.Millisecond, Total: 10 * time.Second})
	ch, err := rt.Chat(context.Background(), ChatRequest{Model: "m"}, DefaultRuntimePolicy())
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainTerminal(t, ch)
	if terminal.EndReason != EndError {
		t.Fatalf("terminal = %v, want EndError", terminal.EndReason)
	}
	if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "首个数据") {
		t.Fatalf("首字节超时文案必须可读：%v", terminal.Err)
	}
}

// 总预算到期 → 文案写明"总时长预算"（绝不伪装成上游错误）。
func TestRuntime_TotalBudgetMessage(t *testing.T) {
	sp := &silentProvider{}
	rt := NewChatRuntime(sp, TimeoutBudget{Total: 100 * time.Millisecond})
	ch, err := rt.Chat(context.Background(), ChatRequest{Model: "m"}, DefaultRuntimePolicy())
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainTerminal(t, ch)
	if terminal.EndReason != EndError {
		t.Fatalf("terminal = %v, want EndError", terminal.EndReason)
	}
	if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "总时长预算") {
		t.Fatalf("总预算超时文案必须写明本地产因：%v", terminal.Err)
	}
}

// 首块到达后首字节预算失效：慢流（首块后静默 250ms）不被首字节计时误杀。
func TestRuntime_FirstByteStopsAfterFirstChunk(t *testing.T) {
	rt := NewChatRuntime(slowStreamProvider{}, TimeoutBudget{FirstByte: 100 * time.Millisecond, Total: 5 * time.Second})
	ch, err := rt.Chat(context.Background(), ChatRequest{Model: "m"}, DefaultRuntimePolicy())
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainTerminal(t, ch)
	if terminal.EndReason != EndDone {
		t.Fatalf("首块后不应再受首字节预算约束：%v / %v", terminal.EndReason, terminal.Err)
	}
}

// 第 4 批：流前失败的重试之间必须有退避（立刻重打对瞬时故障无效，且对上游不友好）。
// 断言取下界（慢机器照常通过）；退避表由测试注入短值。
func TestRuntime_PreStreamRetryBacksOff(t *testing.T) {
	old := RetryBackoff
	RetryBackoff = []time.Duration{60 * time.Millisecond, 120 * time.Millisecond}
	defer func() { RetryBackoff = old }()

	fp := &fakeProvider{connectErrs: 2} // 前两次建流失败，第三次成功
	rt := NewChatRuntime(fp, TimeoutBudget{Total: 5 * time.Second})
	start := time.Now()
	ch, err := rt.Chat(context.Background(), ChatRequest{Model: "m"}, RuntimePolicy{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainTerminal(t, ch)
	if terminal.EndReason != EndDone {
		t.Fatalf("重试后应成功：%v / %v", terminal.EndReason, terminal.Err)
	}
	if fp.callCount() != 3 {
		t.Fatalf("provider calls = %d, want 3", fp.callCount())
	}
	// 60+120=180ms 的注入退避：取下界 150ms（只在方向明确处断言，避免时序假红）
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("两次失败重试之间必须有退避：实际仅 %v", elapsed)
	}
}
