
package loop

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type mockInteractionGateway struct {
	eventChan chan<- EngineEvent
	pending   map[string]chan HumanReply
	mu        sync.Mutex
}

func newMockGateway(eventChan chan<- EngineEvent) *mockInteractionGateway {
	return &mockInteractionGateway{
		eventChan: eventChan,
		pending:   make(map[string]chan HumanReply),
	}
}

func (m *mockInteractionGateway) RequestConfirm(ctx context.Context, sessionID, requestID, toolName, argsPreview, reason string) (bool, error) {
	if m.eventChan != nil {
		m.eventChan <- EngineEvent{
			Type: EventConfirm,
			Confirm: &ConfirmPayload{
				SessionID:   sessionID,
				RequestID:   requestID,
				Tool:        toolName,
				ArgsPreview: argsPreview,
				Reason:      reason,
			},
		}
	}
	m.mu.Lock()
	ch := make(chan HumanReply, 1)
	m.pending[requestID] = ch
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.pending, requestID)
		m.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(200 * time.Millisecond):
		return false, fmt.Errorf("timeout")
	case reply := <-ch:
		return reply.Allow, nil
	}
}

func (m *mockInteractionGateway) RequestChoice(ctx context.Context, sessionID, requestID, question string, options []ChoiceOption) (HumanReply, error) {
	if m.eventChan != nil {
		m.eventChan <- EngineEvent{
			Type: EventChoice,
			Choice: &ChoicePayload{
				SessionID:   sessionID,
				RequestID:   requestID,
				Question:    question,
				Options:     options,
				AllowCustom: true,
			},
		}
	}

	m.mu.Lock()
	ch := make(chan HumanReply, 1)
	m.pending[requestID] = ch
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.pending, requestID)
		m.mu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return HumanReply{}, ctx.Err()
	case <-time.After(200 * time.Millisecond):
		return HumanReply{Timeout: true}, nil
	case reply := <-ch:
		return reply, nil
	}
}

func (m *mockInteractionGateway) DeliverHumanReply(requestID string, reply HumanReply) {
	m.mu.Lock()
	ch, ok := m.pending[requestID]
	m.mu.Unlock()
	if ok {
		select {
		case ch <- reply:
		default:
		}
	}
}

