package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// sharedPeak 跨工具实例的并发峰值计数（并行发生在不同工具实例之间，
// per-instance 计数器永远是 1——必须共享采样）。
type sharedPeak struct {
	inFlight atomic.Int32
	peak     atomic.Int32
}

func (p *sharedPeak) enter() {
	cur := p.inFlight.Add(1)
	for {
		v := p.peak.Load()
		if cur <= v || p.peak.CompareAndSwap(v, cur) {
			break
		}
	}
}

func (p *sharedPeak) exit() { p.inFlight.Add(-1) }

// slowTool 可控延迟的假工具（0.0.09 并行时序测试）。
type slowTool struct {
	name  string
	delay time.Duration
	peak  *sharedPeak
}

func (s *slowTool) Name() string { return s.name }
func (s *slowTool) Description() string {
	return "测试工具：带可控延迟，用于验证并行/串行调度"
}
func (s *slowTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (s *slowTool) Execute(ctx context.Context, args json.RawMessage) (tools.ToolResult, error) {
	s.peak.enter()
	defer s.peak.exit()
	select {
	case <-ctx.Done():
		return tools.ToolResult{Content: "cancelled", IsError: true}, nil
	case <-time.After(s.delay):
	}
	return tools.ToolResult{Content: s.name + " ok", Title: "t", Op: "read"}, nil
}

func scriptCalls(calls ...llm.ToolCallChunk) [][]llm.StreamChunk {
	return [][]llm.StreamChunk{
		{{ToolCalls: calls}, {EndReason: llm.EndDone}},
		{{Delta: "done"}, {EndReason: llm.EndDone}},
	}
}

func chunkCalls(t *testing.T, cs ...llm.ToolCallChunk) []llm.ToolCall {
	t.Helper()
	acc := newCallAccumulator()
	acc.merge(cs)
	return acc.list()
}

// 只读工具连续出现时并发执行：峰值并发 >1（确定性断言）。
// 不再断言墙钟圈速：峰值 ≥2 已经证明执行重叠；而"总耗时 < 串行之和"依赖
// 调度及时性——CI 2 核 runner 全包并行时，真重叠也会被调度拖过阈值
// （实测 3×300ms 跑出 1.1s 而峰值并发正常），墙钟只会制造假红。
func TestAgent_ReadOnlyCallsRunInParallel(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	r1 := &slowTool{name: "search", delay: 300 * time.Millisecond}
	r2 := &slowTool{name: "git", delay: 300 * time.Millisecond}
	r3 := &slowTool{name: "fs", delay: 300 * time.Millisecond} // fs read = 只读白名单
	peak := &sharedPeak{}
	r1.peak, r2.peak, r3.peak = peak, peak, peak
	registry := tools.NewRegistry()
	for _, tl := range []tools.ToolPort{r1, r2, r3} {
		if err := registry.Register(tl); err != nil {
			t.Fatal(err)
		}
	}
	fr := &fakeRuntime{script: scriptCalls(
		llm.ToolCallChunk{Index: 0, ID: "c1", Name: "search", ArgumentsDelta: `{"pattern":"a"}`},
		llm.ToolCallChunk{Index: 1, ID: "c2", Name: "git", ArgumentsDelta: `{"action":"status"}`},
		llm.ToolCallChunk{Index: 2, ID: "c3", Name: "fs", ArgumentsDelta: `{"action":"list"}`},
	)}
	loop := NewLoop(fr, "test-model", registry)

	ch, err := loop.Run(context.Background(), ledger, "read three things")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 15*time.Second)

	if r3.peak.peak.Load() < 2 {
		t.Fatalf("只读工具应并发执行，峰值并发 = %d", r3.peak.peak.Load())
	}
	// 账本事件顺序保持原序（并行只发生在执行，落账按序）
	verifyLedgerOrder(t, dir, []string{"c1", "c2", "c3"})
	// 模型收到的 tool 消息顺序与 calls 一致
	var gotOrder []string
	for _, m := range fr.reqs[1].Messages {
		if m.Role == "tool" {
			gotOrder = append(gotOrder, m.ToolCallID)
		}
	}
	if strings.Join(gotOrder, ",") != "c1,c2,c3" {
		t.Fatalf("tool 消息顺序 = %v", gotOrder)
	}
}

// fs 的 write 不是只读：即使相邻的调用全部写类也必须串行（峰值并发 = 1）。
func TestAgent_WriteCallsStaySerial(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	w1 := &slowTool{name: "fs", delay: 250 * time.Millisecond}
	w1.peak = &sharedPeak{}
	registry := tools.NewRegistry()
	if err := registry.Register(w1); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: scriptCalls(
		llm.ToolCallChunk{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{"action":"write","path":"a"}`},
		llm.ToolCallChunk{Index: 1, ID: "c2", Name: "fs", ArgumentsDelta: `{"action":"replace","path":"a"}`},
	)}
	loop := NewLoop(fr, "test-model", registry)

	start := time.Now()
	ch, err := loop.Run(context.Background(), ledger, "write twice")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)

	if w1.peak.peak.Load() != 1 {
		t.Fatalf("写类调用必须串行，峰值并发 = %d", w1.peak.peak.Load())
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Fatalf("两个 250ms 写调用串行应 ≥500ms，实测 %v（串行纪律被破坏）", elapsed)
	}
}

// 混合批次：[read, write, read] → 并行纪律不得让 write 与任何调用同时执行；
// 且三个调用的账本/消息顺序保持原序。
func TestAgent_MixedBatchKeepsOrderAndSerialWrites(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	r := &slowTool{name: "search", delay: 200 * time.Millisecond}
	w := &slowTool{name: "fs", delay: 200 * time.Millisecond}
	mixed := &sharedPeak{}
	r.peak, w.peak = mixed, mixed
	registry := tools.NewRegistry()
	for _, tl := range []tools.ToolPort{r, w} {
		if err := registry.Register(tl); err != nil {
			t.Fatal(err)
		}
	}
	fr := &fakeRuntime{script: scriptCalls(
		llm.ToolCallChunk{Index: 0, ID: "c1", Name: "search", ArgumentsDelta: `{"pattern":"a"}`},
		llm.ToolCallChunk{Index: 1, ID: "c2", Name: "fs", ArgumentsDelta: `{"action":"write"}`},
		llm.ToolCallChunk{Index: 2, ID: "c3", Name: "search", ArgumentsDelta: `{"pattern":"b"}`},
	)}
	loop := NewLoop(fr, "test-model", registry)

	ch, err := loop.Run(context.Background(), ledger, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)

	// write 的执行与两个 read 都不同段：全过程中 fs 的执行不与 search 重叠
	//（分段串行保证；峰值并发只可能出现在两个 read 分属不同段——本例不相邻，
	// 各自单独成段，因此全程峰值 = 1）
	if w.peak.peak.Load() != 1 {
		t.Fatalf("混合批次不得有任何并行（分段纪律）：峰值 = %d", w.peak.peak.Load())
	}
	verifyLedgerOrder(t, dir, []string{"c1", "c2", "c3"})
}

// verifyLedgerOrder 断言账本里 tool_call 事件按给定的 ID 顺序出现。
func verifyLedgerOrder(t *testing.T, dir string, wantIDs []string) {
	t.Helper()
	l, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var got []string
	if err := l.Replay(func(ev session.Event) error {
		if ev.Kind() != session.EventToolCall {
			return nil
		}
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err != nil {
			return err
		}
		got = append(got, p.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("账本 tool_call 顺序 = %v, want %v", got, wantIDs)
	}
}
