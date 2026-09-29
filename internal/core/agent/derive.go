package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

const toolResultModelLimit = 4096
const toolEventIPCLimit = 64 * 1024

// keepFullToolTurns：最近 N 个 user 轮次的只读工具结果保持全文，更早的收成
// 单行（0.0.09 油表治理）。为什么只折叠只读：read/list/tree/search 的旧输出
// 价值随时间衰减最快（"我读过什么"远不如"读到了什么"重要），而 write/replace
// 的回执与 shell 的失败尾部承载可执行信息，一律不动。为什么不用摘要模型：
// 会把行号和报错写错（0.0.09 用户裁决）——单行丢弃是确定性的，不引入新错误源。
const keepFullToolTurns = 2

func deriveMessages(ledger *session.Ledger) ([]llm.Message, error) {
	// 先数 user 轮次总数：折叠判定需要"最近 N 轮"的边界（账本是单向流，
	// 投影前不知道后面还有没有新 turn）。
	totalTurns := 0
	if err := ledger.Replay(func(ev session.Event) error {
		if ev.Kind() == session.EventUserMessage {
			totalTurns++
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var msgs []llm.Message
	var calls []llm.ToolCall
	results := []*llm.Message{}
	// lastAssistant 指向最近一条纯文本 assistant 锚点（0.0.06）：工具调用前的
	// 助手正文必须随 tool_calls 一起回传给模型——丢掉它，下一轮模型就看不到
	// 自己"为什么"调了工具。锚点只来自 EventAssistantMsg（回合内已确认落盘
	// 的文本）；EventAssistantDelta 的半截内容绝不投影（不把未完成的回复
	// 当成完成回复，thinking 也不进模型上下文）。
	lastAssistant := -1
	turn := -1

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
			turn++
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
				Title   string `json:"title"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			idx := pairToolResult(calls, results, p.ID, p.Name)
			if idx < 0 {
				return nil
			}
			// 油表治理（0.0.09）：非最近轮次的**成功只读**结果收成单行——这是
			// 丢弃旧读数，不是摘要；账本原文不动（账本即事实源）。错误结果与
			// 写类工具永远全文（失败信息与改动回执是可执行信息）。
			isOld := turn < totalTurns-keepFullToolTurns
			isReadOnly := isReadOnlyCall(llm.ToolCall{Name: calls[idx].Name, Arguments: calls[idx].Arguments})
			content := p.Content
			if !p.IsError && isOld && isReadOnly {
				content = foldOldReadOnly(calls[idx].Name, p.Title)
			} else {
				content = truncateToolResult(content)
			}
			results[idx] = &llm.Message{
				Role:       "tool",
				ToolCallID: calls[idx].ID,
				Content:    content,
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

// foldOldReadOnly 把旧轮次的只读结果收成确定性的单行（0.0.09）。
// 明示"已省略"：模型若需要旧内容就重新 read，而不是对被裁的尾巴猜。
func foldOldReadOnly(name, title string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		t = "（无标题）"
	}
	return fmt.Sprintf("%s %s → 已读（旧轮次输出已省略，需要时请重新读取）", name, t)
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
