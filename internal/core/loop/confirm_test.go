package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	

	"tiancode/internal/host"
	"tiancode/internal/llm"
	v1 "tiancode/pkg/plugin/v1"
)

type dangerousMockProvider struct {
	calls int
}
func (m *dangerousMockProvider) ID() string          { return "mock.prov" }
func (m *dangerousMockProvider) Name() string        { return "Mock Provider" }
func (m *dangerousMockProvider) Version() string     { return "1.0.0" }
func (m *dangerousMockProvider) Type() v1.PluginType { return v1.TypeProvider }
func (m *dangerousMockProvider) Init(ctx context.Context, cfg json.RawMessage) error { return nil }
func (m *dangerousMockProvider) Start(ctx context.Context) error { return nil }
func (m *dangerousMockProvider) Stop(ctx context.Context) error  { return nil }
func (m *dangerousMockProvider) Health(ctx context.Context) v1.HealthStatus { return v1.HealthStatus{Healthy: true} }
func (m *dangerousMockProvider) Ping(ctx context.Context) (time.Duration, error) { return 10 * time.Millisecond, nil }
func (m *dangerousMockProvider) ListModels(ctx context.Context) ([]v1.ModelDescriptor, error) { return nil, nil }
func (m *dangerousMockProvider) StreamChat(ctx context.Context, req *v1.ChatRequest) (<-chan v1.StreamChunk, error) {
	ch := make(chan v1.StreamChunk, 2)
	m.calls++
	if m.calls == 1 {
		ch <- v1.StreamChunk{
			ToolCalls: []v1.ToolCallChunk{
				{Index: 0, ID: "call_1", Name: "exec_command", ArgumentsDelta: `{"command":"rm -rf /"}`},
			},
		}
	} else {
		ch <- v1.StreamChunk{DeltaContent: "Final Answer"}
	}
	close(ch)
	return ch, nil
}

type confirmMockRail struct {}
func (m *confirmMockRail) ID() string          { return "mock.rail" }
func (m *confirmMockRail) Name() string        { return "Mock Rail" }
func (m *confirmMockRail) Version() string     { return "1.0.0" }
func (m *confirmMockRail) Type() v1.PluginType { return v1.TypeRail }
func (m *confirmMockRail) Init(ctx context.Context, cfg json.RawMessage) error { return nil }
func (m *confirmMockRail) Start(ctx context.Context) error { return nil }
func (m *confirmMockRail) Stop(ctx context.Context) error  { return nil }
func (m *confirmMockRail) Health(ctx context.Context) v1.HealthStatus { return v1.HealthStatus{Healthy: true} }
func (m *confirmMockRail) Ping(ctx context.Context) (time.Duration, error) { return 10 * time.Millisecond, nil }
func (m *confirmMockRail) Priority() int       { return 100 }
func (m *confirmMockRail) OnBeforeObserve(ctx context.Context, sessionID string) error { return nil }
func (m *confirmMockRail) OnBeforeReason(ctx context.Context, sessionID string, prompt *string) error { return nil }
func (m *confirmMockRail) OnBeforeAct(ctx context.Context, sessionID string, toolName string, args []byte) (*v1.RailDecision, error) {
	if toolName == "exec_command" && strings.Contains(string(args), "rm") {
		return &v1.RailDecision{Allow: false, Intercepted: true, NeedsConfirm: true, Reason: "dangerous rm blocked"}, nil
	}
	return &v1.RailDecision{Allow: true}, nil
}
func (m *confirmMockRail) OnAfterAct(ctx context.Context, sessionID string, toolName string, result *v1.ToolResult) error { return nil }
func (m *confirmMockRail) OnVerify(ctx context.Context, sessionID string) (passed bool, feedback string, err error) { return true, "", nil }

type mockTool struct {
	name   string
	output string
}
func (m *mockTool) ID() string          { return "mock.tool." + m.name }
func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Version() string     { return "1.0.0" }
func (m *mockTool) Type() v1.PluginType { return v1.TypeTool }
func (m *mockTool) Init(ctx context.Context, cfg json.RawMessage) error { return nil }
func (m *mockTool) Start(ctx context.Context) error { return nil }
func (m *mockTool) Stop(ctx context.Context) error  { return nil }
func (m *mockTool) Health(ctx context.Context) v1.HealthStatus { return v1.HealthStatus{Healthy: true} }
func (m *mockTool) Ping(ctx context.Context) (time.Duration, error) { return 10 * time.Millisecond, nil }
func (m *mockTool) Definition() v1.ToolDefinition { return v1.ToolDefinition{Name: m.name} }
func (m *mockTool) Execute(ctx context.Context, args json.RawMessage) (*v1.ToolResult, error) {
	return &v1.ToolResult{Content: m.output}, nil
}

func TestEngine_ConfirmPausesUntilResume_Allow(t *testing.T) {
	reg := host.NewRegistry()
	_ = reg.Register(&dangerousMockProvider{})
	_ = reg.Register(&confirmMockRail{})

	if err := reg.Register(&mockTool{name: "exec_command", output: "success rm"}); err != nil {
		t.Fatalf("failed to register mock tool: %v", err)
	}

	eventChan := make(chan EngineEvent, 100)
	gw := newMockGateway(eventChan)
	engine := NewExecutionEngine(reg, gw, nil, nil)
	
	req := &EngineRequest{
		SessionID: "sess_confirm_1",
		Messages:  []llm.Message{{Role: "user", Content: "Hello"}},
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	done := make(chan bool)
	go func() {
		_ = engine.Execute(ctx, req, eventChan)
		close(done)
	}()
	
	var confirmEvent *ConfirmPayload
	for ev := range eventChan {
		if ev.Type == EventConfirm {
			confirmEvent = ev.Confirm
			break
		}
	}
	
	if confirmEvent == nil {
		t.Fatalf("expected EventConfirm, got nil")
	}
	
	gw.DeliverHumanReply(confirmEvent.RequestID, HumanReply{
		Allow: true,
	})
	
	<-done
	
	var toolOut string
	for ev := range eventChan {
		if ev.Type == EventToolEnd && ev.ToolName == "exec_command" {
			toolOut = ev.ToolOutput
		}
	}
	if !strings.Contains(toolOut, "success rm") {
		t.Fatalf("expected tool output to contain success rm, got: %s", toolOut)
	}
}

func TestEngine_ConfirmPausesUntilResume_Deny(t *testing.T) {
	reg := host.NewRegistry()
	_ = reg.Register(&dangerousMockProvider{})
	_ = reg.Register(&confirmMockRail{})
	_ = reg.Register(&mockTool{name: "exec_command", output: "success rm"})

	eventChan := make(chan EngineEvent, 100)
	gw := newMockGateway(eventChan)
	engine := NewExecutionEngine(reg, gw, nil, nil)
	
	req := &EngineRequest{
		SessionID: "sess_confirm_2",
		Messages:  []llm.Message{{Role: "user", Content: "Hello"}},
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	done := make(chan bool)
	go func() {
		_ = engine.Execute(ctx, req, eventChan)
		close(done)
	}()
	
	for ev := range eventChan {
		if ev.Type == EventConfirm {
			gw.DeliverHumanReply(ev.Confirm.RequestID, HumanReply{
				Allow: false,
			})
			break
		}
	}
	
	<-done
	var toolOut string
	for ev := range eventChan {
		if ev.Type == EventToolEnd && ev.ToolName == "exec_command" {
			toolOut = ev.ToolOutput
		}
	}
	if !strings.Contains(toolOut, "用户拒绝") {
		t.Fatalf("expected tool output to contain user deny, got: %s", toolOut)
	}
}

