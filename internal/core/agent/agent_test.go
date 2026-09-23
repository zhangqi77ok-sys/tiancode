package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
)

// fakeRuntime 在 ChatRuntime 边界注入可控的流与请求捕获。
type fakeRuntime struct {
	mu   sync.Mutex
	reqs []llm.ChatRequest
	ch   chan llm.StreamChunk
	err  error
}

func (f *fakeRuntime) Chat(ctx context.Context, req llm.ChatRequest, _ llm.RuntimePolicy) (<-chan llm.StreamChunk, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	ch := f.ch
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return ch, nil
}

func (f *fakeRuntime) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func newTestLedger(t *testing.T) (*session.Ledger, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	return l, dir
}

// drain 读取流直至关闭或超时。
func drain(t *testing.T, ch <-chan llm.StreamChunk, timeout time.Duration) []llm.StreamChunk {
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
			t.Fatalf("stream did not close within %v", timeout)
		}
	}
}

// Phase 状态机：Idle → Running（轮内）→ Idle（轮毕）。
func TestAgent_PhaseTransitions(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	fch := make(chan llm.StreamChunk)
	loop := NewLoop(&fakeRuntime{ch: fch}, "test-model")
	if loop.Phase() != PhaseIdle {
		t.Fatalf("initial phase = %v, want Idle", loop.Phase())
	}

	ch, err := loop.Run(context.Background(), ledger, "q")
	if err != nil {
		t.Fatal(err)
	}
	if loop.Phase() != PhaseRunning {
		t.Fatalf("phase during turn = %v, want Running", loop.Phase())
	}

	close(fch) // runtime 未给终态即关闭 → agent 兜底 EndError（端口契约被破坏时的防线）
	drain(t, ch, 3*time.Second)
	if loop.Phase() != PhaseIdle {
		t.Fatalf("phase after turn = %v, want Idle", loop.Phase())
	}
}

// 忙碌拒绝：单轮进行中再次 Run 必须报错（防并发轮次撕裂账本语义）。
func TestAgent_BusyRejected(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	fch := make(chan llm.StreamChunk)
	loop := NewLoop(&fakeRuntime{ch: fch}, "m")
	ch, err := loop.Run(context.Background(), ledger, "q")
	if err != nil {
		t.Fatal(err)
	}
	defer drain(t, ch, 3*time.Second)
	defer close(fch)

	if _, err := loop.Run(context.Background(), ledger, "q2"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Run err = %v, want ErrBusy", err)
	}
}

// C-APP-1（前置层）：用户消息持久化失败必须同步上抛。
func TestAgent_PersistErrorPropagates(t *testing.T) {
	ledger, _ := newTestLedger(t)
	ledger.Close() // 注入持久化失败

	loop := NewLoop(&fakeRuntime{}, "m")
	if _, err := loop.Run(context.Background(), ledger, "q"); err == nil {
		t.Fatal("persist failure must propagate")
	}
}

// C-APP-1（流中层）：增量落盘失败 → EndError 终态上抛，且 EndDone 不再被转发（恰好一个终态）。
func TestAgent_DeltaPersistErrorBecomesTerminal(t *testing.T) {
	ledger, _ := newTestLedger(t)

	fch := make(chan llm.StreamChunk, 2)
	fch <- llm.StreamChunk{Delta: "hi"}
	fch <- llm.StreamChunk{EndReason: llm.EndDone}
	close(fch)

	loop := NewLoop(&fakeRuntime{ch: fch}, "m")
	ch, err := loop.Run(context.Background(), ledger, "q") // user 消息落盘成功
	if err != nil {
		t.Fatal(err)
	}
	ledger.Close() // 注入后续持久化失败

	chunks := drain(t, ch, 3*time.Second)
	terminals := 0
	sawPersistErr := false
	for _, c := range chunks {
		if c.EndReason != llm.EndNone {
			terminals++
			if c.Err != nil && strings.Contains(c.Err.Error(), "ledger closed") {
				sawPersistErr = true
			}
		}
	}
	if terminals != 1 {
		t.Fatalf("terminals = %d, want 1", terminals)
	}
	if !sawPersistErr {
		t.Fatal("expected EndError carrying persistence failure")
	}
}

// C-APP-2：取消 → EndCancelled 终态 + 已产生的 delta 保留在账本 + 不写 assistant_message 锚点。
func TestAgent_CancelKeepsEvents(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	fch := make(chan llm.StreamChunk)
	loop := NewLoop(&fakeRuntime{ch: fch}, "m")
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := loop.Run(ctx, ledger, "q")
	if err != nil {
		t.Fatal(err)
	}

	fch <- llm.StreamChunk{Delta: "partial"}
	<-ch // 消费方收到首块后取消（模拟用户点中断）
	cancel()
	fch <- llm.StreamChunk{EndReason: llm.EndCancelled, Err: context.Canceled}
	drain(t, ch, 3*time.Second)

	l2, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	deltas, anchors := 0, 0
	if err := l2.Replay(func(ev session.SessionEvent) error {
		switch ev.Kind() {
		case session.EventAssistantDelta:
			deltas++
		case session.EventAssistantMsg:
			anchors++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if deltas == 0 {
		t.Fatal("cancelled turn must keep produced deltas")
	}
	if anchors != 0 {
		t.Fatal("incomplete turn must not write assistant_message anchor")
	}
}

// 会话恢复：历史从账本重放推导（user_message/assistant_message → 多轮消息）。
func TestAgent_HistoryFromLedger(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "u1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": "a1"}); err != nil {
		t.Fatal(err)
	}

	fch := make(chan llm.StreamChunk, 2)
	fch <- llm.StreamChunk{Delta: "ok"}
	fch <- llm.StreamChunk{EndReason: llm.EndDone}
	close(fch)
	fr := &fakeRuntime{ch: fch}
	loop := NewLoop(fr, "test-model")

	ch, err := loop.Run(context.Background(), ledger, "q2")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	if fr.requestCount() != 1 {
		t.Fatalf("requests = %d, want 1", fr.requestCount())
	}
	msgs := fr.reqs[0].Messages
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3 (u1,a1,q2)", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "u1" {
		t.Fatalf("msg[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "a1" {
		t.Fatalf("msg[1] = %+v", msgs[1])
	}
	if msgs[2].Role != "user" || msgs[2].Content != "q2" {
		t.Fatalf("msg[2] = %+v", msgs[2])
	}
	if fr.reqs[0].Model != "test-model" {
		t.Fatalf("model = %q", fr.reqs[0].Model)
	}
}
