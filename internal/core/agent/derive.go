package agent

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
)

const toolResultModelLimit = 4096
const toolEventIPCLimit = 64 * 1024

func deriveMessages(ledger *session.Ledger) ([]llm.Message, error) {
	var msgs []llm.Message
	var calls []llm.ToolCall
	results := []*llm.Message{}

	complete := func() bool {
		if len(calls) == 0 {
			return false
		}
		for _, r := range results {
			if r == nil {
				return false
			}
		}
		return len(results) == len(calls)
	}
	flush := func() {
		if !complete() {
			calls, results = nil, nil
			return
		}
		msgs = append(msgs, llm.Message{Role: "assistant", ToolCalls: append([]llm.ToolCall(nil), calls...)})
		for _, r := range results {
			msgs = append(msgs, *r)
		}
		calls, results = nil, nil
	}

	err := ledger.Replay(func(ev session.Event) error {
		switch ev.Kind() {
		case session.EventUserMessage:
			flush()
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			msgs = append(msgs, llm.Message{Role: "user", Content: p.Text})
		case session.EventToolCall:
			if complete() {
				flush()
			}
			var p struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			id := p.ID
			if id == "" {
				id = fmt.Sprintf("call-%d", ev.Seq())
			}
			calls = append(calls, llm.ToolCall{ID: id, Name: p.Name, Arguments: p.Arguments})
			results = append(results, nil)
		case session.EventToolResult:
			var p struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Content string `json:"content"`
				IsError bool   `json:"is_error"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			idx := pairToolResult(calls, results, p.ID, p.Name)
			if idx < 0 {
				return nil
			}
			results[idx] = &llm.Message{
				Role:       "tool",
				ToolCallID: calls[idx].ID,
				Content:    truncateToolResult(p.Content),
			}
		case session.EventAssistantMsg:
			flush()
			var p struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			msgs = append(msgs, llm.Message{Role: "assistant", Content: p.Text})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	flush()
	return msgs, nil
}

func pairToolResult(calls []llm.ToolCall, results []*llm.Message, id, name string) int {
	if id != "" {
		for i, c := range calls {
			if c.ID == id && results[i] == nil {
				return i
			}
		}
	}
	if name != "" {
		for i, c := range calls {
			if results[i] == nil && c.Name == name {
				return i
			}
		}
	}
	for i, r := range results {
		if r == nil {
			return i
		}
	}
	return -1
}

func truncateToolResult(content string) string {
	if len(content) <= toolResultModelLimit {
		return content
	}
	return truncateToBytes(content, toolResultModelLimit) + fmt.Sprintf("\n\n[truncated, original %d bytes]", len(content))
}

func truncateToBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
