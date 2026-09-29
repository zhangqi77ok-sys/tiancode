package agent

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

const toolResultModelLimit = 4096
const toolEventIPCLimit = 64 * 1024

func deriveMessages(ledger *session.Ledger) ([]llm.Message, error) {
	var msgs []llm.Message
	var calls []llm.ToolCall
	results := []*llm.Message{}
	// lastAssistant 指向最近一条纯文本 assistant 锚点（0.0.06）：工具调用前的
	// 助手正文必须随 tool_calls 一起回传给模型——丢掉它，下一轮模型就看不到
	// 自己"为什么"调了工具。锚点只来自 EventAssistantMsg（回合内已确认落盘
	// 的文本）；EventAssistantDelta 的半截内容绝不投影（不把未完成的回复
	// 当成完成回复，thinking 也不进模型上下文）。
	lastAssistant := -1

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
		if lastAssistant >= 0 && lastAssistant < len(msgs) &&
			msgs[lastAssistant].Role == "assistant" &&
			len(msgs[lastAssistant].ToolCalls) == 0 {
			// 工具调用前的助手正文：并入同一条 assistant(text, tool_calls)
			msgs[lastAssistant].ToolCalls = append([]llm.ToolCall(nil), calls...)
		} else {
			msgs = append(msgs, llm.Message{Role: "assistant", ToolCalls: append([]llm.ToolCall(nil), calls...)})
		}
		for _, r := range results {
			msgs = append(msgs, *r)
		}
		calls, results = nil, nil
		lastAssistant = -1
	}

	err := ledger.Replay(func(ev session.Event) error {
		switch ev.Kind() {
		case session.EventUserMessage:
			flush()
			lastAssistant = -1
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
			lastAssistant = len(msgs) - 1
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
	// 0.0.06 头尾保留：工具结果里模型最需要的信息常在尾部（测试 FAIL 汇总、
	// 命令最终错误、diff 末尾）。只留头部会让模型对着开头猜结局。
	return tools.HeadTail(content, toolResultModelLimit)
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
