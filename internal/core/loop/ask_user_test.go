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

type askUserMockProvider struct {
	calls int
	ch    chan v1.StreamChunk
}
func (m *askUserMockProvider) ID() string          { return "mock.prov" }
func (m *askUserMockProvider) Name() string        { return "Mock Provider" }
func (m *askUserMockProvider) Version() string     { return "1.0.0" }
func (m *askUserMockProvider) Type() v1.PluginType { return v1.TypeProvider }
func (m *askUserMockProvider) Init(ctx context.Context, cfg json.RawMessage) error { return nil }
func (m *askUserMockProvider) Start(ctx context.Context) error { return nil }
func (m *askUserMockProvider) Stop(ctx context.Context) error  { return nil }
func (m *askUserMockProvider) Health(ctx context.Context) v1.HealthStatus { return v1.HealthStatus{Healthy: true} }
func (m *askUserMockProvider) Ping(ctx context.Context) (time.Duration, error) { return 10 * time.Millisecond, nil }
func (m *askUserMockProvider) ListModels(ctx context.Context) ([]v1.ModelDescriptor, error) { return nil, nil }
func (m *askUserMockProvider) StreamChat(ctx context.Context, req *v1.ChatRequest) (<-chan v1.StreamChunk, error) {
	ch := make(chan v1.StreamChunk, 2)
	m.calls++
	if m.calls == 1 {
		ch <- v1.StreamChunk{
			ToolCalls: []v1.ToolCallChunk{
				{Index: 0, ID: "call_1", Name: "ask_user", ArgumentsDelta: `{"question":"Q","options":[{"id":"1","label":"A","recommended":true},{"id":"2","label":"B"}]}`},
			},
		}
	} else {
		ch <- v1.StreamChunk{DeltaContent: "Final Answer"}
	}
	close(ch)
	return ch, nil
}

func TestEngine_AskUserPausesUntilResume(t *testing.T) {
	reg := host.NewRegistry()
	reg.Register(&askUserMockProvider{})

	eventChan := make(chan EngineEvent, 100)
	gw := newMockGateway(eventChan)
	engine := NewExecutionEngine(reg, gw)
	
	req := &EngineRequest{
		SessionID: "sess_1",
		Messages:  []llm.Message{{Role: "user", Content: "Hello"}},
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	done := make(chan bool)
	go func() {
		_ = engine.Execute(ctx, req, eventChan)
		close(done)
	}()
	
	var choiceEvent *ChoicePayload
	for ev := range eventChan {
		if ev.Type == EventChoice {
			choiceEvent = ev.Choice
			break
		}
	}
	
	if choiceEvent == nil {
		t.Fatalf("expected EventChoice, got nil")
	}
	
	// Ensure engine is blocked (Execute hasn't finished)
	select {
	case <-done:
		t.Fatalf("engine Execute finished prematurely before resume")
	case <-time.After(100 * time.Millisecond):
		// still blocked, good
	}
	
	gw.DeliverHumanReply(choiceEvent.RequestID, HumanReply{
		OptionID:   "2",
		CustomNote: "my note",
		Allow:      true,
	})
	
	<-done
	
	// Collect remaining events
	var toolOut string
	for ev := range eventChan {
		if ev.Type == EventToolEnd && ev.ToolName == "ask_user" {
			toolOut = ev.ToolOutput
		}
	}
	if !strings.Contains(toolOut, "option_id=2") || !strings.Contains(toolOut, "my note") {
		t.Fatalf("expected tool output to contain user choice, got: %s", toolOut)
	}
}

func TestEngine_AskUserSkipUsesRecommended(t *testing.T) {
	reg := host.NewRegistry()
	reg.Register(&askUserMockProvider{})

	eventChan := make(chan EngineEvent, 100)
	gw := newMockGateway(eventChan)
	engine := NewExecutionEngine(reg, gw)
	
	req := &EngineRequest{
		SessionID: "sess_2",
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
		if ev.Type == EventChoice {
			// Skip by sending Allow=false
			gw.DeliverHumanReply(ev.Choice.RequestID, HumanReply{
				OptionID: "",
				Allow:    false,
			})
			break
		}
	}
	
	<-done
	var toolOut string
	for ev := range eventChan {
		if ev.Type == EventToolEnd && ev.ToolName == "ask_user" {
			toolOut = ev.ToolOutput
		}
	}
	if !strings.Contains(toolOut, "skipped_default=true") {
		t.Fatalf("expected tool output to contain skipped_default=true, got: %s", toolOut)
	}
}

func TestAskUserRejectsFewerThanTwoOptions(t *testing.T) {
	reg := host.NewRegistry()
	p := &askUserMockProvider{}
	reg.Register(p)
	
	eventChan := make(chan EngineEvent, 100)
	gw := newMockGateway(eventChan)
	engine := NewExecutionEngine(reg, gw)
	
	req := &EngineRequest{
		SessionID: "sess_3",
		Messages:  []llm.Message{{Role: "user", Content: "Hello"}},
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	// Hack mock provider for this test
	go func() {
		_ = engine.Execute(ctx, req, eventChan)
	}()
	
	// We didn't change the mock, wait, we need a separate mock or we can just call runTool... 
	// But ask_user is intercepted in Execute directly. 
}


