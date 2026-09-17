package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tiancode/internal/host"
	v1 "tiancode/pkg/plugin/v1"
	"tiancode/plugins/rail/safety"
)

type mockProvider struct {
	calls int
}

func (m *mockProvider) ID() string          { return "mock.prov" }
func (m *mockProvider) Name() string        { return "Mock Provider" }
func (m *mockProvider) Version() string     { return "1.0.0" }
func (m *mockProvider) Type() v1.PluginType { return v1.TypeProvider }
func (m *mockProvider) Init(ctx context.Context, cfg json.RawMessage) error { return nil }
func (m *mockProvider) Start(ctx context.Context) error { return nil }
func (m *mockProvider) Stop(ctx context.Context) error  { return nil }
func (m *mockProvider) Health(ctx context.Context) v1.HealthStatus {
	return v1.HealthStatus{Healthy: true}
}
func (m *mockProvider) Ping(ctx context.Context) (time.Duration, error) {
	return 10 * time.Millisecond, nil
}
func (m *mockProvider) ListModels(ctx context.Context) ([]v1.ModelDescriptor, error) {
	return nil, nil
}
func (m *mockProvider) StreamChat(ctx context.Context, req *v1.ChatRequest) (<-chan v1.StreamChunk, error) {
	ch := make(chan v1.StreamChunk, 2)
	m.calls++
	if m.calls == 1 {
		// 第 1 轮：模型返回非 0 开始的稀疏工具调用 (index=1)
		ch <- v1.StreamChunk{
			ToolCalls: []v1.ToolCallChunk{
				{
					Index:          1, // 稀疏索引测试
					ID:             "call_sparse_1",
					Name:           "non_existent_tool",
					ArgumentsDelta: `{"foo":"bar"}`,
				},
			},
		}
	} else {
		// 第 2 轮：模型返回最终结果，结束循环
		ch <- v1.StreamChunk{
			DeltaContent: "Done!",
		}
	}
	close(ch)
	return ch, nil
}

func TestExecutionEngine_SparseToolIndices(t *testing.T) {
	reg := host.NewRegistry()
	prov := &mockProvider{}
	if err := reg.Register(prov); err != nil {
		t.Fatalf("failed to register provider: %v", err)
	}

	gw := newMockGateway(nil)
	engine := NewExecutionEngine(reg, gw)
	eventChan := make(chan EngineEvent, 20)

	ctx := context.Background()
	req := &EngineRequest{
		Model:  "mock-model",
		Prompt: "test",
	}

	go func() {
		_ = engine.Execute(ctx, req, eventChan)
	}()

	receivedToolStart := false
	for ev := range eventChan {
		if ev.Type == EventToolStart && ev.ToolCallID == "call_sparse_1" {
			receivedToolStart = true
		}
	}

	if !receivedToolStart {
		t.Errorf("expected tool_start event for sparse index 1, but not received")
	}
}

type loopInfiniteProvider struct{}

func (m *loopInfiniteProvider) ID() string          { return "mock.loop" }
func (m *loopInfiniteProvider) Name() string        { return "Loop Provider" }
func (m *loopInfiniteProvider) Version() string     { return "1.0.0" }
func (m *loopInfiniteProvider) Type() v1.PluginType { return v1.TypeProvider }
func (m *loopInfiniteProvider) Init(ctx context.Context, cfg json.RawMessage) error { return nil }
func (m *loopInfiniteProvider) Start(ctx context.Context) error { return nil }
func (m *loopInfiniteProvider) Stop(ctx context.Context) error  { return nil }
func (m *loopInfiniteProvider) Health(ctx context.Context) v1.HealthStatus {
	return v1.HealthStatus{Healthy: true}
}
func (m *loopInfiniteProvider) Ping(ctx context.Context) (time.Duration, error) {
	return 10 * time.Millisecond, nil
}
func (m *loopInfiniteProvider) ListModels(ctx context.Context) ([]v1.ModelDescriptor, error) {
	return nil, nil
}
func (m *loopInfiniteProvider) StreamChat(ctx context.Context, req *v1.ChatRequest) (<-chan v1.StreamChunk, error) {
	ch := make(chan v1.StreamChunk, 1)
	ch <- v1.StreamChunk{
		ToolCalls: []v1.ToolCallChunk{
			{
				Index:          0,
				ID:             "call_infinite",
				Name:           "tool.none",
				ArgumentsDelta: "{}",
			},
		},
	}
	close(ch)
	return ch, nil
}


func TestExecutionEngine_NilGuards(t *testing.T) {
	// 1. nil engine
	var nilEngine *ExecutionEngine
	ch1 := make(chan EngineEvent, 10)
	err1 := nilEngine.Execute(context.Background(), &EngineRequest{Prompt: "hi"}, ch1)
	if err1 == nil {
		t.Fatalf("expected error on nil engine, got nil")
	}

	// 2. nil registry
	engineNoReg := NewExecutionEngine(nil, nil)
	ch2 := make(chan EngineEvent, 10)
	err2 := engineNoReg.Execute(context.Background(), &EngineRequest{Prompt: "hi"}, ch2)
	if err2 == nil {
		t.Fatalf("expected error on engine with nil registry, got nil")
	}

	// 3. nil request
	reg := host.NewRegistry()
	engineOk := NewExecutionEngine(reg, nil)
	ch3 := make(chan EngineEvent, 10)
	err3 := engineOk.Execute(context.Background(), nil, ch3)
	if err3 == nil {
		t.Fatalf("expected error on nil request, got nil")
	}
}

type dangerousProvider struct{ n int }

func (m *dangerousProvider) ID() string          { return "mock.danger" }
func (m *dangerousProvider) Name() string        { return "Danger" }
func (m *dangerousProvider) Version() string     { return "1.0.0" }
func (m *dangerousProvider) Type() v1.PluginType { return v1.TypeProvider }
func (m *dangerousProvider) Init(context.Context, json.RawMessage) error { return nil }
func (m *dangerousProvider) Start(context.Context) error                 { return nil }
func (m *dangerousProvider) Stop(context.Context) error                  { return nil }
func (m *dangerousProvider) Health(context.Context) v1.HealthStatus {
	return v1.HealthStatus{Healthy: true}
}
func (m *dangerousProvider) Ping(context.Context) (time.Duration, error) {
	return time.Millisecond, nil
}
func (m *dangerousProvider) ListModels(context.Context) ([]v1.ModelDescriptor, error) {
	return nil, nil
}
func (m *dangerousProvider) StreamChat(ctx context.Context, req *v1.ChatRequest) (<-chan v1.StreamChunk, error) {
	ch := make(chan v1.StreamChunk, 1)
	m.n++
	if m.n == 1 {
		ch <- v1.StreamChunk{ToolCalls: []v1.ToolCallChunk{{
			Index: 0, ID: "c1", Name: "exec_command", ArgumentsDelta: `{"command":"rm -rf /"}`,
		}}}
	} else {
		ch <- v1.StreamChunk{DeltaContent: "stopped"}
	}
	close(ch)
	return ch, nil
}

func TestExecutionEngine_SafetyRailBlocks(t *testing.T) {
	reg := host.NewRegistry()
	_ = reg.Register(&dangerousProvider{})
	_ = reg.Register(safety.New())
	gw := newMockGateway(nil)
	engine := NewExecutionEngine(reg, gw)
	ch := make(chan EngineEvent, 20)
	go func() { _ = engine.Execute(context.Background(), &EngineRequest{Prompt: "x"}, ch) }()
	blocked := false
	for ev := range ch {
		if ev.Type == EventToolEnd && strings.Contains(ev.ToolOutput, "安全拦截") {
			blocked = true
		}
	}
	if !blocked {
		t.Fatal("SafetyRail should block rm -rf")
	}
}

type tddWriteProvider struct{ calls int }

func (m *tddWriteProvider) ID() string          { return "mock.tdd" }
func (m *tddWriteProvider) Name() string        { return "tdd" }
func (m *tddWriteProvider) Version() string     { return "1" }
func (m *tddWriteProvider) Type() v1.PluginType { return v1.TypeProvider }
func (m *tddWriteProvider) Init(ctx context.Context, cfg json.RawMessage) error { return nil }
func (m *tddWriteProvider) Start(ctx context.Context) error { return nil }
func (m *tddWriteProvider) Stop(ctx context.Context) error  { return nil }
func (m *tddWriteProvider) Health(ctx context.Context) v1.HealthStatus {
	return v1.HealthStatus{Healthy: true}
}
func (m *tddWriteProvider) Ping(ctx context.Context) (time.Duration, error) {
	return 0, nil
}
func (m *tddWriteProvider) ListModels(ctx context.Context) ([]v1.ModelDescriptor, error) {
	return nil, nil
}
func (m *tddWriteProvider) StreamChat(ctx context.Context, req *v1.ChatRequest) (<-chan v1.StreamChunk, error) {
	ch := make(chan v1.StreamChunk, 2)
	m.calls++
	if m.calls == 1 {
		ch <- v1.StreamChunk{ToolCalls: []v1.ToolCallChunk{{
			Index: 0, ID: "c1", Name: "fs_control", ArgumentsDelta: `{"action":"write","path":"a.go","content":"x"}`,
		}}}
	} else {
		ch <- v1.StreamChunk{DeltaContent: "done"}
	}
	close(ch)
	return ch, nil
}

type tddWriteTool struct{}

func (t *tddWriteTool) ID() string          { return "tool.fs.mock" }
func (t *tddWriteTool) Name() string        { return "fs" }
func (t *tddWriteTool) Version() string     { return "1" }
func (t *tddWriteTool) Type() v1.PluginType { return v1.TypeTool }
func (t *tddWriteTool) Init(ctx context.Context, cfg json.RawMessage) error { return nil }
func (t *tddWriteTool) Start(ctx context.Context) error                    { return nil }
func (t *tddWriteTool) Stop(ctx context.Context) error                     { return nil }
func (t *tddWriteTool) Health(ctx context.Context) v1.HealthStatus {
	return v1.HealthStatus{Healthy: true}
}
func (t *tddWriteTool) Definition() v1.ToolDefinition {
	return v1.ToolDefinition{Name: "fs_control", Description: "fs"}
}
func (t *tddWriteTool) Execute(ctx context.Context, args json.RawMessage) (*v1.ToolResult, error) {
	return &v1.ToolResult{Content: "written", IsError: false}, nil
}

func TestExecutionEngine_TDDVerifyAfterWrite(t *testing.T) {
	reg := host.NewRegistry()
	_ = reg.Register(&tddWriteProvider{})
	_ = reg.Register(&tddWriteTool{})
	gw := newMockGateway(nil)
	engine := NewExecutionEngine(reg, gw)
	called := ""
	engine.Verify = func(written string) (string, bool) {
		called = written
		return "ok tests", true
	}
	ch := make(chan EngineEvent, 20)
	go func() {
		_ = engine.Execute(context.Background(), &EngineRequest{Prompt: "x", Strategy: StrategyTDD}, ch)
	}()
	saw := false
	for ev := range ch {
		if ev.Type == EventToolEnd && strings.Contains(ev.ToolOutput, "TDD 验证") && strings.Contains(ev.ToolOutput, "a.go") {
			saw = true
		}
	}
	if called != "a.go" {
		t.Fatalf("Verify file=%q", called)
	}
	if !saw {
		t.Fatal("expected TDD follow-up in tool output")
	}
}

func TestExecutionEngine_DirectPathUsesRegisteredProvider(t *testing.T) {
	reg := host.NewRegistry()
	prov := &mockProvider{}
	if err := reg.Register(prov); err != nil {
		t.Fatal(err)
	}
	gw := newMockGateway(nil)
	engine := NewExecutionEngine(reg, gw)
	ch := make(chan EngineEvent, 30)
	go func() {
		_ = engine.Execute(context.Background(), &EngineRequest{
			Prompt:   "x",
			APIKey:   "fake-api-key-0123456789abcdef",
			Endpoint: "https://example.invalid/v1",
			Model:    "local-model",
		}, ch)
	}()
	gotDone := false
	for ev := range ch {
		if ev.Type == EventDone {
			gotDone = true
		}
	}
	if !gotDone {
		t.Fatal("expected provider-backed loop to finish")
	}
	if prov.calls < 1 {
		t.Fatal("registered provider was not used")
	}
}

type loopConsecutiveErrorProvider struct {
	turn int
}

func (m *loopConsecutiveErrorProvider) ID() string          { return "mock.errors" }
func (m *loopConsecutiveErrorProvider) Name() string        { return "Error Provider" }
func (m *loopConsecutiveErrorProvider) Version() string     { return "1.0.0" }
func (m *loopConsecutiveErrorProvider) Type() v1.PluginType { return v1.TypeProvider }
func (m *loopConsecutiveErrorProvider) Init(ctx context.Context, cfg json.RawMessage) error {
	return nil
}
func (m *loopConsecutiveErrorProvider) Start(ctx context.Context) error { return nil }
func (m *loopConsecutiveErrorProvider) Stop(ctx context.Context) error  { return nil }
func (m *loopConsecutiveErrorProvider) Health(ctx context.Context) v1.HealthStatus {
	return v1.HealthStatus{Healthy: true}
}
func (m *loopConsecutiveErrorProvider) Ping(ctx context.Context) (time.Duration, error) {
	return 10 * time.Millisecond, nil
}
func (m *loopConsecutiveErrorProvider) ListModels(ctx context.Context) ([]v1.ModelDescriptor, error) {
	return nil, nil
}
func (m *loopConsecutiveErrorProvider) StreamChat(ctx context.Context, req *v1.ChatRequest) (<-chan v1.StreamChunk, error) {
	ch := make(chan v1.StreamChunk, 2)
	m.turn++
	ch <- v1.StreamChunk{
		ToolCalls: []v1.ToolCallChunk{
			{
				Index:          0,
				ID:             "err_call",
				Name:           "missing_tool_" + string(rune('0'+m.turn)),
				ArgumentsDelta: `{"arg":"val"}`,
			},
		},
	}
	close(ch)
	return ch, nil
}

func TestExecutionEngine_CircuitBreaker_DuplicateCalls(t *testing.T) {
	reg := host.NewRegistry()
	prov := &loopInfiniteProvider{}
	if err := reg.Register(prov); err != nil {
		t.Fatalf("register: %v", err)
	}
	gw := newMockGateway(nil)
	engine := NewExecutionEngine(reg, gw)
	eventChan := make(chan EngineEvent, 80)
	go func() {
		_ = engine.Execute(context.Background(), &EngineRequest{
			Model:    "mock-model",
			Prompt:   "test duplicate breaker",
			APIKey:   "k",
			Endpoint: "http://127.0.0.1:9",
		}, eventChan)
	}()
	var chunks strings.Builder
	for ev := range eventChan {
		if ev.Type == EventChunk {
			chunks.WriteString(ev.DeltaContent)
		}
	}
	got := chunks.String()
	if !strings.Contains(got, "防死循环熔断") {
		t.Fatalf("expected duplicate tool call circuit breaker triggered, got %q", got)
	}
}

func TestExecutionEngine_CircuitBreaker_ConsecutiveErrors(t *testing.T) {
	reg := host.NewRegistry()
	prov := &loopConsecutiveErrorProvider{}
	if err := reg.Register(prov); err != nil {
		t.Fatalf("register: %v", err)
	}
	gw := newMockGateway(nil)
	engine := NewExecutionEngine(reg, gw)
	eventChan := make(chan EngineEvent, 80)
	go func() {
		_ = engine.Execute(context.Background(), &EngineRequest{
			Model:    "mock-model",
			Prompt:   "test error breaker",
			APIKey:   "k",
			Endpoint: "http://127.0.0.1:9",
		}, eventChan)
	}()
	var chunks strings.Builder
	for ev := range eventChan {
		if ev.Type == EventChunk {
			chunks.WriteString(ev.DeltaContent)
		}
	}
	got := chunks.String()
	if !strings.Contains(got, "连续失败熔断") {
		t.Fatalf("expected consecutive errors circuit breaker triggered, got %q", got)
	}
}

