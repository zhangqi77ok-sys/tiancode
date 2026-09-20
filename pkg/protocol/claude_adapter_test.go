package protocol

import (
	"encoding/json"
	"testing"

	v1 "tiancode/pkg/plugin/v1"
)

func TestConvertToolsToClaude_WithCacheBreakpoint(t *testing.T) {
	tools := []v1.ToolDefinition{
		{
			Name:        "search",
			Description: "Search code",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		},
		{
			Name:        "write",
			Description: "Write file",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		},
	}

	claudeTools := ConvertToolsToClaude(tools, true)
	if len(claudeTools) != 2 {
		t.Fatalf("Expected 2 tools, got %d", len(claudeTools))
	}

	if claudeTools[0].CacheControl != nil {
		t.Errorf("First tool should not have cache_control")
	}

	if claudeTools[1].CacheControl == nil || claudeTools[1].CacheControl.Type != "ephemeral" {
		t.Errorf("Last tool must have ephemeral cache_control")
	}
}

func TestInjectClaudeMessageCacheBreakpoint(t *testing.T) {
	msgs := []ClaudeMessage{
		{
			Role:    "user",
			Content: []ClaudeBlock{{Type: "text", Text: "Hello Turn 1"}},
		},
		{
			Role:    "assistant",
			Content: []ClaudeBlock{{Type: "text", Text: "Answer Turn 1"}},
		},
		{
			Role:    "user",
			Content: []ClaudeBlock{{Type: "text", Text: "Followup Turn 2"}},
		},
		{
			Role:    "assistant",
			Content: []ClaudeBlock{{Type: "text", Text: "Answer Turn 2"}},
		},
		{
			Role:    "user",
			Content: []ClaudeBlock{{Type: "text", Text: "Latest Turn 3"}},
		},
	}

	InjectClaudeMessageCacheBreakpoint(msgs)

	// Turn 1 user should have no breakpoint
	if msgs[0].Content[0].CacheControl != nil {
		t.Errorf("Turn 1 user should not have cache breakpoint")
	}

	// Turn 2 user (N-2) should have ephemeral breakpoint
	if msgs[2].Content[0].CacheControl == nil || msgs[2].Content[0].CacheControl.Type != "ephemeral" {
		t.Errorf("Turn 2 user (turn N-2) must have ephemeral cache breakpoint")
	}

	// Turn 3 user (latest) should NOT have breakpoint
	if msgs[4].Content[0].CacheControl != nil {
		t.Errorf("Turn 3 user (latest) must NOT have cache breakpoint")
	}
}
