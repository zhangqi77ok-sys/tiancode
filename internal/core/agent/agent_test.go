package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// fakeRuntime 按脚本回放每次 Chat 调用的流（脚本段依序消费，段间独立）。
type fakeRuntime struct {
	mu     sync.Mutex
	reqs   []llm.ChatRequest
	script [][]llm.StreamChunk
	err    error
	manual chan llm.StreamChunk // 非空时忽略脚本（测试手工控制时序）
}

func (f *fakeRuntime) Chat(_ context.Context, req llm.ChatRequest, _ llm.RuntimePolicy) (<-chan llm.StreamChunk, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	n := len(f.reqs)
	manual := f.manual
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if manual != nil {
		return manual, nil
	}
	if n > len(f.script) {
		return nil, fmt.Errorf("unexpected Chat call #%d (script has %d)", n, len(f.script))
	}
	seg := f.script[n-1]
	ch := make(chan llm.StreamChunk, len(seg))
	for _, c := range seg {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func (f *fakeRuntime) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

// scriptTool 返回固定结果的桩工具。
type scriptTool struct {
	name     string
	result   tools.ToolResult
	executed int
}

func (s *scriptTool) Name() string        { return s.name }
func (s *scriptTool) Description() string { return "scripted " + s.name }
func (s *scriptTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (s *scriptTool) Execute(_ context.Context, _ json.RawMessage) (tools.ToolResult, error) {
	s.executed++
	return s.result, nil
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

func countEvents(t *testing.T, dir string, kind session.EventKind) int {
	t.Helper()
	l, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	n := 0
	if err := l.Replay(func(ev session.Event) error {
		if ev.Kind() == kind {
			n++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// Phase 状态机：Idle → Running（轮内）→ Idle（轮毕）。
func TestAgent_PhaseTransitions(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "x"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "test-model", nil)
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
	drain(t, ch, 3*time.Second)
	if loop.Phase() != PhaseIdle {
		t.Fatalf("phase after turn = %v, want Idle", loop.Phase())
	}
}

// 忙碌拒绝：单轮进行中再次 Run 必须报 ErrBusy（防并发轮次撕裂账本语义）。
func TestAgent_BusyRejected(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	fch := make(chan llm.StreamChunk)
	fr := &fakeRuntime{manual: fch}
	loop := NewLoop(fr, "m", nil)
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

	loop := NewLoop(&fakeRuntime{}, "m", nil)
	if _, err := loop.Run(context.Background(), ledger, "q"); err == nil {
		t.Fatal("persist failure must propagate")
	}
}

// C-APP-1（流中层）：增量落盘失败 → EndError 终态上抛，且 EndDone 不再被转发。
func TestAgent_DeltaPersistErrorBecomesTerminal(t *testing.T) {
	ledger, _ := newTestLedger(t)

	fch := make(chan llm.StreamChunk, 2)
	fch <- llm.StreamChunk{Delta: "hi"}
	fch <- llm.StreamChunk{EndReason: llm.EndDone}
	close(fch)

	loop := NewLoop(&fakeRuntime{manual: fch}, "m", nil)
	ch, err := loop.Run(context.Background(), ledger, "q") // user 消息落盘成功
	if err != nil {
		t.Fatal(err)
	}
	ledger.Close() // 注入后续持久化失败

	chunks := drain(t, ch, 3*time.Second)
	terminals, sawPersistErr := 0, false
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

// C-APP-2：取消 → EndCancelled 终态 + 已产生 delta 保留账本 + 不写锚点。
func TestAgent_CancelKeepsEvents(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "partial"}, {EndReason: llm.EndCancelled, Err: context.Canceled}},
	}}
	loop := NewLoop(fr, "m", nil)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := loop.Run(ctx, ledger, "q")
	if err != nil {
		t.Fatal(err)
	}
	<-ch // 收到首块后取消（模拟用户点中断）
	cancel()
	drain(t, ch, 3*time.Second)

	if deltas := countEvents(t, dir, session.EventAssistantDelta); deltas == 0 {
		t.Fatal("cancelled turn must keep produced deltas")
	}
	if anchors := countEvents(t, dir, session.EventAssistantMsg); anchors != 0 {
		t.Fatal("incomplete turn must not write assistant_message anchor")
	}
}

// C-RT-4：agent 只依赖 ChatRuntime 抽象，不感知渠道与重试的存在（Facade 边界）。
//
// 为什么值得单独锁一条：这是"换渠道 / 调重试策略无需改动内核"的结构性保证。
// 分两层验证，缺一不可：
//  1. 编译期证据——用一个与真实实现毫无关系的替身驱动内核跑完一整轮。若 agent 依赖了
//     具体运行时或渠道类型，本测试根本无法通过编译。
//  2. 静态边界——本包源码不得出现适配器/编排/壳层依赖。这是守卫 R1 的用例级镜像：
//     让边界在 `go test` 里也能被证伪，而不只在提交前的手工脚本里。
func TestRuntime_FacadeBoundary(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	// 1) 接口依赖：替身运行时足以驱动完整一轮（产出 assistant 锚点）
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "ok"}, {EndReason: llm.EndDone}},
	}}
	ch, err := NewLoop(fr, "test-model", nil).Run(context.Background(), ledger, "q")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)
	if n := countEvents(t, dir, session.EventAssistantMsg); n != 1 {
		t.Fatalf("assistant 锚点数 = %d, want 1（替身运行时应当能驱动完整一轮）", n)
	}

	// 2) 静态边界：只扫**非测试**源码——产线边界才是要守的东西。
	//    为什么跳过 *_test.go：测试文件合法地需要更多引用，而且本文件自身就带着
	//    下面这些禁止依赖的字面量（扫描字符串必然命中自己），会造成自指误报。
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"tiancode/internal/platform", // 适配器：渠道/provider/工具实现
		"tiancode/internal/app",      // 编排层
		"tiancode/app",               // 壳层（Wails 绑定）
		"wailsapp/wails",             // UI 框架
	}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		for _, bad := range forbidden {
			if strings.Contains(string(src), `"`+bad) {
				t.Errorf("%s 出现禁止依赖 %q：内核不得感知适配器/编排/壳层", e.Name(), bad)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("未扫描到任何产线源码，测试前提不成立")
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

	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "ok"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "test-model", nil)

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

// 工具往返：模型调用工具 → agent 执行 → 结果回填 → 续步出最终回答。
func TestAgent_ToolRoundtrip(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "fs", result: tools.ToolResult{Content: "file-x"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{Delta: "checking"},
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{"action":"read"}`}}},
			{EndReason: llm.EndDone},
		},
		{
			{Delta: "it says file-x"},
			{EndReason: llm.EndDone},
		},
	}}
	loop := NewLoop(fr, "test-model", registry)

	ch, err := loop.Run(context.Background(), ledger, "read a.txt")
	if err != nil {
		t.Fatal(err)
	}
	chunks := drain(t, ch, 3*time.Second)

	toolEvents, terminal := 0, llm.StreamChunk{}
	for _, c := range chunks {
		if c.ToolEvent != nil {
			toolEvents++
			if c.ToolEvent.Name != "fs" || c.ToolEvent.Status != "success" || c.ToolEvent.Summary != "file-x" {
				t.Fatalf("tool event = %+v", c.ToolEvent)
			}
		}
		if c.EndReason != llm.EndNone {
			terminal = c
		}
	}
	if toolEvents != 1 {
		t.Fatalf("tool events = %d, want 1", toolEvents)
	}
	if terminal.EndReason != llm.EndDone {
		t.Fatalf("terminal = %v, want EndDone", terminal.EndReason)
	}
	if st.executed != 1 {
		t.Fatalf("tool executed = %d, want 1", st.executed)
	}

	// 第二次请求必须携带 assistant(tool_calls) + tool 结果
	if fr.requestCount() != 2 {
		t.Fatalf("requests = %d, want 2", fr.requestCount())
	}
	msgs := fr.reqs[1].Messages
	if len(msgs) != 3 {
		t.Fatalf("step-2 messages = %d, want 3", len(msgs))
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != "c1" {
		t.Fatalf("msgs[1] = %+v", msgs[1])
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "c1" || msgs[2].Content != "file-x" {
		t.Fatalf("msgs[2] = %+v", msgs[2])
	}

	// 账本：tool_call / tool_result 各一条；锚点是最终回答
	if n := countEvents(t, dir, session.EventToolCall); n != 1 {
		t.Fatalf("tool_call events = %d, want 1", n)
	}
	if n := countEvents(t, dir, session.EventToolResult); n != 1 {
		t.Fatalf("tool_result events = %d, want 1", n)
	}
	if n := countEvents(t, dir, session.EventAssistantMsg); n != 1 {
		t.Fatalf("assistant anchors = %d, want 1", n)
	}
}

// 未知工具：业务失败回填模型，循环继续而非崩溃。
func TestAgent_UnknownToolContinues(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "nope", ArgumentsDelta: "{}"}}},
			{EndReason: llm.EndDone},
		},
		{
			{Delta: "fallback"},
			{EndReason: llm.EndDone},
		},
	}}
	loop := NewLoop(fr, "m", tools.NewRegistry()) // 空注册表

	ch, err := loop.Run(context.Background(), ledger, "q")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	if fr.requestCount() != 2 {
		t.Fatalf("requests = %d, want 2 (must continue after unknown tool)", fr.requestCount())
	}
	msgs := fr.reqs[1].Messages
	if len(msgs) != 3 || msgs[2].Content != `unknown tool "nope"` {
		t.Fatalf("step-2 messages = %+v", msgs)
	}
	if n := countEvents(t, dir, session.EventToolResult); n != 1 {
		t.Fatalf("tool_result events = %d, want 1", n)
	}
	if n := countEvents(t, dir, session.EventAssistantMsg); n != 1 {
		t.Fatalf("assistant anchors = %d, want 1", n)
	}
}

// 步数上限：模型持续调用工具，MaxStepsPerTurn 步后以 EndError 收束，不再调用。
func TestAgent_StepLimit(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "loop", result: tools.ToolResult{Content: "again"}}
	var script [][]llm.StreamChunk
	for i := 0; i < MaxStepsPerTurn+5; i++ {
		script = append(script, []llm.StreamChunk{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: fmt.Sprintf("c%d", i), Name: "loop", ArgumentsDelta: "{}"}}},
			{EndReason: llm.EndDone},
		})
	}
	fr := &fakeRuntime{script: script}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(fr, "m", registry)

	ch, err := loop.Run(context.Background(), ledger, "q")
	if err != nil {
		t.Fatal(err)
	}
	chunks := drain(t, ch, 5*time.Second)

	terminal := llm.StreamChunk{}
	for _, c := range chunks {
		if c.EndReason != llm.EndNone {
			terminal = c
		}
	}
	if terminal.EndReason != llm.EndError {
		t.Fatalf("terminal = %v, want EndError", terminal.EndReason)
	}
	if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "step limit") {
		t.Fatalf("terminal err = %v, want step limit", terminal.Err)
	}
	if fr.requestCount() != MaxStepsPerTurn {
		t.Fatalf("requests = %d, want %d", fr.requestCount(), MaxStepsPerTurn)
	}
	if n := countEvents(t, dir, session.EventAssistantMsg); n != 0 {
		t.Fatalf("assistant anchors = %d, want 0 (turn incomplete)", n)
	}
}
