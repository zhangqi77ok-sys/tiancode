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

	st := &scriptTool{name: "fs", result: tools.ToolResult{
		Content: "file-x", Title: "a.txt", Op: "read",
		Diff: "--- a.txt\n+++ a.txt\n@@ -1,1 +1,1 @@\n-old\n+new",
	}}
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
			// 0.0.06：第一条是 running 事件（CallID 同 c1），第二条才是成功终态
			if toolEvents == 1 {
				if c.ToolEvent.Status != "running" || c.ToolEvent.CallID != "c1" {
					t.Fatalf("first tool event = %+v, want running/c1", c.ToolEvent)
				}
				continue
			}
			if c.ToolEvent.Name != "fs" || c.ToolEvent.Status != "success" || c.ToolEvent.Summary != "file-x" || c.ToolEvent.Content != "file-x" {
				t.Fatalf("tool event = %+v", c.ToolEvent)
			}
			// UI 语义标签与结构化 diff 必须透传到实时事件（工具卡"install.go（修改）"的数据源）
			if c.ToolEvent.Title != "a.txt" || c.ToolEvent.Op != "read" || c.ToolEvent.Diff == "" {
				t.Fatalf("tool event title/op/diff = %q/%q/%q", c.ToolEvent.Title, c.ToolEvent.Op, c.ToolEvent.Diff)
			}
		}
		if c.EndReason != llm.EndNone {
			terminal = c
		}
	}
	if toolEvents != 2 {
		t.Fatalf("tool events = %d, want 2（running + success）", toolEvents)
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

	// 账本：tool_call / tool_result 各一条；锚点 = 工具前正文（0.0.06 新增）+ 最终回答
	if n := countEvents(t, dir, session.EventToolCall); n != 1 {
		t.Fatalf("tool_call events = %d, want 1", n)
	}
	if n := countEvents(t, dir, session.EventToolResult); n != 1 {
		t.Fatalf("tool_result events = %d, want 1", n)
	}
	if n := countEvents(t, dir, session.EventAssistantMsg); n != 2 {
		t.Fatalf("assistant anchors = %d, want 2 (工具前正文 + 最终回答)", n)
	}

	// 账本 payload 必须携带 title/op/diff——Replay 恢复历史工具卡的数据源
	// （此前 diff 不落账，历史卡片丢失"变更预览"）
	l2, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	var got struct{ title, op, diff string }
	if err := l2.Replay(func(ev session.Event) error {
		if ev.Kind() != session.EventToolResult {
			return nil
		}
		var p struct {
			Title string `json:"title"`
			Op    string `json:"op"`
			Diff  string `json:"diff"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err != nil {
			return err
		}
		got.title, got.op, got.diff = p.Title, p.Op, p.Diff
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got.title != "a.txt" || got.op != "read" || got.diff == "" {
		t.Fatalf("ledger payload title=%q op=%q diff=%q", got.title, got.op, got.diff)
	}
}

// ToolEvent.Content 超过 64KiB 必须截断（壳层 IPC）；Summary 仍是 200 字节摘要。
func TestAgent_TruncatesToolEventForIPC(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	big := strings.Repeat("x", 70*1024)
	st := &scriptTool{name: "fs", result: tools.ToolResult{Content: big}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "done"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)
	ch, err := loop.Run(context.Background(), ledger, "q")
	if err != nil {
		t.Fatal(err)
	}
	chunks := drain(t, ch, 3*time.Second)

	var ev *llm.ToolEvent
	for _, c := range chunks {
		if c.ToolEvent != nil {
			ev = c.ToolEvent
		}
	}
	if ev == nil {
		t.Fatal("missing ToolEvent")
	}
	wantSummary := strings.Repeat("x", 200) + "…"
	if ev.Summary != wantSummary {
		t.Fatalf("summary len=%d want 200-byte chip, got %q", len(ev.Summary), ev.Summary)
	}
	if ev.Content == big {
		t.Fatal("ToolEvent.Content must be truncated for IPC")
	}
	if !strings.Contains(ev.Content, "middle bytes omitted") {
		t.Fatalf("content missing head/tail truncate marker: len=%d", len(ev.Content))
	}
	// 0.0.06 头尾保留：头部在（2/5 预算）且尾部也在
	if !strings.HasPrefix(ev.Content, strings.Repeat("x", toolEventIPCLimit*2/5)) {
		t.Fatal("truncated IPC content must keep the head")
	}
	if !strings.HasSuffix(ev.Content, strings.Repeat("x", 50)) {
		t.Fatal("truncated IPC content must keep the tail")
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

// 步数上限（0.0.06 语义）：正常路径已由 TestAgent_StepLimitForcesSummaryInsteadOfError
// 覆盖（强制总结步 → EndDone）。这里锁死唯一走 EndError 的路径：总结步的模型
// 调用本身失败（脚本耗尽 = 模拟上游错误）。
func TestAgent_StepLimit_SummaryCallFailureIsError(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "loop", result: tools.ToolResult{Content: "again"}}
	var script [][]llm.StreamChunk
	for i := 0; i < MaxStepsPerTurn; i++ {
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
		t.Fatalf("terminal = %v, want EndError (总结步失败)", terminal.EndReason)
	}
	if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "step limit") {
		t.Fatalf("terminal err = %v, want step limit", terminal.Err)
	}
	if fr.requestCount() != MaxStepsPerTurn+1 {
		t.Fatalf("requests = %d, want %d（25 步工具 + 1 步总结）", fr.requestCount(), MaxStepsPerTurn+1)
	}
	if n := countEvents(t, dir, session.EventAssistantMsg); n != 0 {
		t.Fatalf("assistant anchors = %d, want 0 (turn incomplete)", n)
	}
}

// 模型省略 tool call ID 时，同轮续步消息里 assistant.ToolCalls 与 role=tool 必须共用合成 ID。
func TestAgent_FillsEmptyToolCallIDInSameTurn(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "fs", result: tools.ToolResult{Content: "ok"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "", Name: "fs", ArgumentsDelta: `{}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "done"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)
	ch, err := loop.Run(context.Background(), ledger, "q")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	if fr.requestCount() != 2 {
		t.Fatalf("requests = %d, want 2", fr.requestCount())
	}
	msgs := fr.reqs[1].Messages
	if len(msgs) < 3 {
		t.Fatalf("step-2 messages = %+v", msgs)
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("msgs[1] = %+v", msgs[1])
	}
	id := msgs[1].ToolCalls[0].ID
	if id == "" {
		t.Fatal("assistant ToolCalls[0].ID must be synthesized when model omits id")
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != id || msgs[2].Content != "ok" {
		t.Fatalf("msgs[2] = %+v, want ToolCallID %q", msgs[2], id)
	}
}

// C-AGT-1：第二轮 Run 发给模型的消息必须含上一轮 assistant(tool_calls)+role=tool。
func TestAgent_DerivesToolHistoryAcrossTurns(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "fs", result: tools.ToolResult{Content: "file-x"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{"action":"read"}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "fixed"}, {EndReason: llm.EndDone}},
		{{Delta: "ok"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)

	ch, err := loop.Run(context.Background(), ledger, "fix foo")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	ch, err = loop.Run(context.Background(), ledger, "also bar")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	if fr.requestCount() != 3 {
		t.Fatalf("requests = %d, want 3", fr.requestCount())
	}
	msgs := fr.reqs[2].Messages
	if len(msgs) != 5 {
		t.Fatalf("turn-2 messages = %d, want 5 (user, assistant+tools, tool, assistant, user); got %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Content != "fix foo" {
		t.Fatalf("msgs[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != "c1" {
		t.Fatalf("msgs[1] = %+v", msgs[1])
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "c1" || msgs[2].Content != "file-x" {
		t.Fatalf("msgs[2] = %+v", msgs[2])
	}
	if msgs[3].Role != "assistant" || msgs[3].Content != "fixed" {
		t.Fatalf("msgs[3] = %+v", msgs[3])
	}
	if msgs[4].Role != "user" || msgs[4].Content != "also bar" {
		t.Fatalf("msgs[4] = %+v", msgs[4])
	}
}

// C-AGT-2：发给模型的单条 tool 结果超过 4096 字节必须截断（0.0.06 头尾保留）；
// 账本保留全文。
func TestAgent_TruncatesToolResultForModel(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	big := strings.Repeat("x", 5000) + "\nFAIL: tail-must-survive"
	st := &scriptTool{name: "fs", result: tools.ToolResult{Content: big}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "done"}, {EndReason: llm.EndDone}},
		{{Delta: "ok"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)
	ch, err := loop.Run(context.Background(), ledger, "q1")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)
	ch, err = loop.Run(context.Background(), ledger, "q2")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	got := fr.reqs[2].Messages[2].Content
	if len(got) >= len(big) || !strings.Contains(got, "middle bytes omitted") {
		t.Fatalf("model tool content = %d bytes, %q", len(got), got[:min(80, len(got))])
	}
	// 头部保留（约 2/5）且尾部 FAIL 汇总必须可见——只留头部会让模型猜结局
	if !strings.HasPrefix(got, strings.Repeat("x", 1000)) {
		t.Fatal("truncated view must keep the head")
	}
	if !strings.Contains(got, "FAIL: tail-must-survive") {
		t.Fatalf("尾部 FAIL 汇总被截没：%s", got[len(got)-120:])
	}

	l2, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	full := ""
	if err := l2.Replay(func(ev session.Event) error {
		if ev.Kind() != session.EventToolResult {
			return nil
		}
		var p struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err != nil {
			return err
		}
		full = p.Content
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if full != big {
		t.Fatalf("ledger content len = %d, want %d", len(full), len(big))
	}
}

// C-AGT-3：没有 result 的 tool_call 不得进入模型消息。
func TestAgent_OmitsUnpairedToolCall(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventToolCall, map[string]string{
		"id": "c-orphan", "name": "fs", "arguments": "{}",
	}); err != nil {
		t.Fatal(err)
	}

	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "ok"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", nil)
	ch, err := loop.Run(context.Background(), ledger, "new")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	for i, m := range fr.reqs[0].Messages {
		if m.Role == "tool" || len(m.ToolCalls) > 0 {
			t.Fatalf("unpaired tool leaked at msgs[%d] = %+v", i, m)
		}
	}
}

// C-AGT-4：旧账本缺 id 时合成 call-{seq} 且配对合法。
func TestAgent_SyntheticIDsForLegacyLedger(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "old"}); err != nil {
		t.Fatal(err)
	}
	callEv, err := ledger.Append(session.EventToolCall, map[string]string{
		"name": "fs", "arguments": "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventToolResult, map[string]any{
		"name": "fs", "content": "ok", "is_error": false,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": "done"}); err != nil {
		t.Fatal(err)
	}

	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "ok"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", nil)
	ch, err := loop.Run(context.Background(), ledger, "next")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	msgs := fr.reqs[0].Messages
	wantID := fmt.Sprintf("call-%d", callEv.Seq())
	if len(msgs) < 4 {
		t.Fatalf("messages = %+v", msgs)
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != wantID {
		t.Fatalf("msgs[1] = %+v, want id %s", msgs[1], wantID)
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != wantID || msgs[2].Content != "ok" {
		t.Fatalf("msgs[2] = %+v", msgs[2])
	}
}

// todo 工具由 Loop 按名拦截：落 EventTodo、实时推 TodoEvent、结果回填模型上下文。
func TestAgent_TodoRoundtrip(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	registry := tools.NewRegistry()
	if err := registry.Register(NewTodoTool()); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "t1", Name: "todo", ArgumentsDelta: `{"items":[{"text":"a","status":"pending"}]}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "planned"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)
	ch, err := loop.Run(context.Background(), ledger, "plan")
	if err != nil {
		t.Fatal(err)
	}
	chunks := drain(t, ch, 3*time.Second)

	todoEvents := 0
	for _, c := range chunks {
		if c.Todo != nil {
			todoEvents++
			if len(c.Todo.Items) != 1 || c.Todo.Items[0].Text != "a" || c.Todo.Items[0].Status != "pending" {
				t.Fatalf("todo event = %+v", c.Todo)
			}
		}
	}
	if todoEvents != 1 {
		t.Fatalf("todo events = %d, want 1", todoEvents)
	}
	// 结果回填模型上下文（工具轮合法续步）
	msgs := fr.reqs[1].Messages
	if len(msgs) < 3 || msgs[2].Role != "tool" || !strings.Contains(msgs[2].Content, "todo list updated") {
		t.Fatalf("step-2 messages = %+v", msgs)
	}
	// EventTodo 落账
	l2, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	n := 0
	if err := l2.Replay(func(ev session.Event) error {
		if ev.Kind() != session.EventTodo {
			return nil
		}
		n++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("EventTodo count = %d, want 1", n)
	}
}

// ask_user 由 Loop 拦截：问题/选项经 Asker 端口到 UI，答案作为工具结果回填模型。
func TestAgent_AskRoundtrip(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	registry := tools.NewRegistry()
	if err := registry.Register(NewAskUserTool()); err != nil {
		t.Fatal(err)
	}
	stub := &stubAsker{answer: "方案 A"}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "a1", Name: "ask_user", ArgumentsDelta: `{"question":"选哪个方案？","options":["方案 A","方案 B"]}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "按方案 A 继续"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)
	loop.SetAsker(stub)
	ch, err := loop.Run(context.Background(), ledger, "问一下")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	if stub.got.Question != "选哪个方案？" || len(stub.got.Options) != 2 {
		t.Fatalf("asker got = %+v", stub.got)
	}
	msgs := fr.reqs[1].Messages
	if len(msgs) < 3 || msgs[2].Role != "tool" || msgs[2].Content != "方案 A" {
		t.Fatalf("step-2 messages = %+v", msgs)
	}
}

// asker 未注入：ask_user 收到引导性结果（非错误），模型据此自行决策而不是反复重试。
func TestAgent_AskWithoutChannel(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	registry := tools.NewRegistry()
	if err := registry.Register(NewAskUserTool()); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "a1", Name: "ask_user", ArgumentsDelta: `{"question":"q"}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "自行决定"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry) // 无 SetAsker
	ch, err := loop.Run(context.Background(), ledger, "q")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)
	if !strings.Contains(fr.reqs[1].Messages[2].Content, "unavailable") {
		t.Fatalf("tool content = %q", fr.reqs[1].Messages[2].Content)
	}
}

type stubAsker struct {
	answer string
	got    AskRequest
}

func (s *stubAsker) Ask(_ context.Context, req AskRequest) (string, error) {
	s.got = req
	return s.answer, nil
}
