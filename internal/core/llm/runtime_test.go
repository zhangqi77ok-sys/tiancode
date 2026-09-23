package llm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProvider 在端口契约边界上注入三种故障：前置连接失败 / 流中错误 / 连接挂起。
type fakeProvider struct {
	mu          sync.Mutex
	calls       int
	connectErrs int           // 前 N 次 StreamChat 返回前置错误（模拟连接失败）
	streamErr   error         // 流建立后注入 EndError 终态（模拟流中失败）
	blockFor    time.Duration // StreamChat 阻塞时长（模拟连接挂起，尊重 ctx）
}

func (f *fakeProvider) StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()

	if f.blockFor > 0 {
		select {
		case <-time.After(f.blockFor):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if n <= f.connectErrs {
		return nil, errors.New("connection refused")
	}
	ch := make(chan StreamChunk, 2)
	if f.streamErr != nil {
		ch <- StreamChunk{EndReason: EndError, Err: f.streamErr}
	} else {
		ch <- StreamChunk{Delta: "hi"}
		ch <- StreamChunk{EndReason: EndDone}
	}
	close(ch)
	return ch, nil
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// C-RT-1：流开始前的失败按 MaxAttempts 重试，对调用方透明。
func TestRuntime_PreStreamRetry(t *testing.T) {
	fp := &fakeProvider{connectErrs: 1}
	rt := NewChatRuntime(fp, TimeoutBudget{Total: 2 * time.Second})

	ch, err := rt.Chat(context.Background(), ChatRequest{Model: "m"}, RuntimePolicy{MaxAttempts: 2})
	if err != nil {
		t.Fatalf("Chat should succeed after pre-stream retry: %v", err)
	}
	var terminal StreamChunk
	for c := range ch {
		if c.EndReason != EndNone {
			terminal = c
		}
	}
	if terminal.EndReason != EndDone {
		t.Fatalf("terminal = %v, want EndDone", terminal.EndReason)
	}
	if fp.callCount() != 2 {
		t.Fatalf("provider calls = %d, want 2 (1 fail + 1 success)", fp.callCount())
	}
}

// C-RT-2：流建立后的失败绝不重试（防上下文撕裂），EndError 原样透传。
func TestRuntime_NoRetryMidStream(t *testing.T) {
	fp := &fakeProvider{streamErr: errors.New("boom mid-stream")}
	rt := NewChatRuntime(fp, TimeoutBudget{Total: 2 * time.Second})

	ch, err := rt.Chat(context.Background(), ChatRequest{Model: "m"}, RuntimePolicy{MaxAttempts: 3})
	if err != nil {
		t.Fatalf("stream established: Chat must not fail: %v", err)
	}
	var terminal StreamChunk
	for c := range ch {
		if c.EndReason != EndNone {
			terminal = c
		}
	}
	if terminal.EndReason != EndError || terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "boom") {
		t.Fatalf("terminal = %+v, want EndError with boom", terminal)
	}
	if fp.callCount() != 1 {
		t.Fatalf("provider calls = %d, want 1 (mid-stream failure must not retry)", fp.callCount())
	}
}

// C-RT-3：Runtime 施加总时长预算——连接挂起时按预算返回错误，不永久阻塞。
func TestRuntime_TimeoutBudget(t *testing.T) {
	fp := &fakeProvider{blockFor: 2 * time.Second}
	rt := NewChatRuntime(fp, TimeoutBudget{Total: 60 * time.Millisecond})

	start := time.Now()
	_, err := rt.Chat(context.Background(), ChatRequest{Model: "m"}, RuntimePolicy{MaxAttempts: 2})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("budget expiry must return error")
	}
	if elapsed > time.Second {
		t.Fatalf("budget not enforced: took %v", elapsed)
	}
	if fp.callCount() != 2 {
		t.Fatalf("provider calls = %d, want 2 (each attempt within budget)", fp.callCount())
	}
}
