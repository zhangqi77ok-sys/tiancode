// Package agent 是 ReAct 执行内核：LLM 生成 → 工具调用 → 结果回填 → 续步。
//
// 做什么：无状态的多步对话执行循环。会话状态由 core/session 账本持有
// （write-ahead：事件先落账本再上抛 UI），模型访问经 core/llm.ChatRuntime，
// 工具执行经 core/tools 端口——本包自身不做任何直接外部访问。
// 被谁依赖：internal/app（编排）。
// 依赖谁：core/llm、core/tools、core/session（端口与类型）。
//
// 边界铁律：Loop 不持有会话文件句柄、不发起 HTTP 请求、不直接实现工具；
// Phase 状态机保证单轮互斥（ADR-0005 State 模式）；
// 端口化与无状态使本包可脱离 UI/磁盘/网络独立测试（TDD 核心）。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// MaxStepsPerTurn 单轮（一次用户消息触发的连续推理）最大步数。
// 为什么 25：足够覆盖常见多步编码任务，同时封顶失控循环的 token 消耗；
// 每步 = 一次模型调用（可能带工具调用）。步数耗尽以 EndError 收束且不写锚点。
const MaxStepsPerTurn = 25

// terminalGrace 是终态块投递等待上限（与 provider 同值同理由）：
// 给排空中的消费方一次阻塞投递机会；已离开时最多延迟 500ms 关闭，杜绝泄漏。
const terminalGrace = 500 * time.Millisecond

// Phase 是轮次状态（ADR-0005 State 模式）。
type Phase int32

const (
	// PhaseIdle 空闲：可接受新轮次。
	PhaseIdle Phase = iota
	// PhaseRunning 轮次进行中。
	PhaseRunning
	// PhaseCancelled 观察到取消终态（收尾中的瞬时状态，随后回 Idle）。
	PhaseCancelled
)

// ErrBusy 在上一轮尚未结束时再次 Run 返回。
var ErrBusy = errors.New("agent is already running a turn")

// Loop 是 ReAct 循环。
type Loop struct {
	runtime  llm.ChatRuntime
	model    string
	registry *tools.Registry // 可为 nil：纯对话模式（无工具）
	phase    atomic.Int32
	// approver 为 nil 表示不启用审批（默认关，ADR-0007）；经 SetApprover 注入
	approver Approver
}

// NewLoop 构造循环：构造期注入运行时、模型与工具注册表（nil = 无工具）。
func NewLoop(rt llm.ChatRuntime, model string, registry *tools.Registry) *Loop {
	return &Loop{runtime: rt, model: model, registry: registry}
}

// Phase 返回当前轮次状态。
func (l *Loop) Phase() Phase { return Phase(l.phase.Load()) }

// Run 执行一轮对话：
//  1. 用户消息落账本（失败同步上抛，C-APP-1）；
//  2. 从账本重放推导多轮消息（会话恢复的核心路径）；
//  3. 经 ChatRuntime 多步执行：模型流式输出 → 工具调用 → 结果回填 → 续步，
//     直至模型给出无工具调用的最终回答（或步数耗尽）。
//
// 返回的通道恰好含一个 EndReason != EndNone 终态块后关闭；
// 工具动态以 ToolEvent 块形式穿插其间。
func (l *Loop) Run(ctx context.Context, ledger *session.Ledger, userText string) (<-chan llm.StreamChunk, error) {
	if !l.phase.CompareAndSwap(int32(PhaseIdle), int32(PhaseRunning)) {
		return nil, ErrBusy
	}
	// 用户消息 write-ahead：持久化成功才允许继续（账本即事实源）
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": userText}); err != nil {
		l.phase.Store(int32(PhaseIdle))
		return nil, fmt.Errorf("persist user message: %w", err)
	}
	msgs, err := l.deriveMessages(ledger)
	if err != nil {
		l.phase.Store(int32(PhaseIdle))
		return nil, fmt.Errorf("derive history: %w", err)
	}
	var toolDefs []llm.ToolDef
	if l.registry != nil {
		toolDefs = l.registry.Definitions()
	}
	out := make(chan llm.StreamChunk)
	go l.turn(ctx, ledger, msgs, toolDefs, out)
	return out, nil
}

// deriveMessages 从账本重放推导多轮消息。
// 只取 user_message / assistant_message 两类锚点：delta 是过程量，assistant_message
// 才是"确认完成"的消息；被取消的轮次天然不进入历史（其 delta 仍留在账本供审计）。
// 注意：进行中的工具调用轮次不落锚点，崩溃恢复后该轮从最后一次用户消息重放。
func (l *Loop) deriveMessages(ledger *session.Ledger) ([]llm.Message, error) {
	var msgs []llm.Message
	err := ledger.Replay(func(ev session.Event) error {
		var p struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err != nil {
			return err
		}
		switch ev.Kind() {
		case session.EventUserMessage:
			msgs = append(msgs, llm.Message{Role: "user", Content: p.Text})
		case session.EventAssistantMsg:
			msgs = append(msgs, llm.Message{Role: "assistant", Content: p.Text})
		}
		return nil
	})
	return msgs, err
}

// turn 是多步消费循环：每步一次模型调用；工具调用触发续步。
func (l *Loop) turn(ctx context.Context, ledger *session.Ledger, msgs []llm.Message, toolDefs []llm.ToolDef, out chan llm.StreamChunk) {
	// defer 顺序即执行顺序（LIFO）：先置 Idle 再 close(out)，
	// 保证消费方见到关闭时 Phase 已回 Idle。
	defer l.phase.Store(int32(PhaseIdle))
	defer close(out)

	var terminalSent bool
	emitTerminal := func(c llm.StreamChunk) {
		if terminalSent {
			return
		}
		terminalSent = true
		if c.EndReason == llm.EndCancelled {
			l.phase.Store(int32(PhaseCancelled))
		}
		select {
		case out <- c:
		case <-time.After(terminalGrace):
		}
	}
	// forward 转发非终态块；消费方离开返回 true（此时直接退出，事件已落账本）
	forward := func(c llm.StreamChunk) bool {
		select {
		case out <- c:
			return false
		case <-ctx.Done():
			return true
		}
	}

	var sb strings.Builder // 当前步已确认落盘的助手文本
	for step := 1; step <= MaxStepsPerTurn; step++ {
		ch, err := l.runtime.Chat(ctx, llm.ChatRequest{
			Model:    l.model,
			Messages: msgs,
			Tools:    toolDefs,
		}, llm.DefaultRuntimePolicy())
		if err != nil {
			emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("model call failed: %w", err)})
			return
		}

		text, calls, terminal, done := l.consumeStream(ctx, ledger, ch, out, &sb)
		if done {
			// 非 EndDone 终态（错误/取消/超时）或端口契约被破坏：透传终态并终止
			//（不写锚点——轮次未完成，已产生 delta 留在账本）
			emitTerminal(terminal)
			return
		}
		if len(calls) == 0 {
			// 最终回答：锚点 write-ahead 后上抛 EndDone
			if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": text}); err != nil {
				emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist assistant message: %w", err)})
				return
			}
			if _, err := ledger.Append(session.EventTurnEnd, map[string]string{"reason": "done"}); err != nil {
				emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist turn end: %w", err)})
				return
			}
			emitTerminal(terminal)
			return
		}

		// 工具调用轮：回填 assistant(tool_calls) + 逐个执行工具并追加 tool 结果消息
		msgs = append(msgs, llm.Message{Role: "assistant", Content: text, ToolCalls: calls})
		for _, call := range calls {
			if _, err := ledger.Append(session.EventToolCall, map[string]string{
				"name": call.Name, "arguments": call.Arguments,
			}); err != nil {
				emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist tool call: %w", err)})
				return
			}
			result := l.execToolWithApproval(ctx, call) // 审批闸门（默认关，ADR-0007）
			if _, err := ledger.Append(session.EventToolResult, map[string]any{
				"name": call.Name, "content": result.Content, "is_error": result.IsError,
			}); err != nil {
				emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist tool result: %w", err)})
				return
			}
			// OpenAI 协议：assistant(tool_calls) 之后必须回填 role=tool 结果消息，
			// 下一续步请求才合法（结果经 ToolCallID 与调用配对）
			msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: call.ID, Content: result.Content})
			status, summary := "success", result.Content
			if result.IsError {
				status = "error"
			}
			if len(summary) > 200 {
				// 为什么截断 200：工具卡片只需摘要，完整结果已在账本与模型上下文中
				summary = summary[:200] + "…"
			}
			if forward(llm.StreamChunk{ToolEvent: &llm.ToolEvent{
				Name: call.Name, Status: status, Summary: summary, Diff: result.Diff,
			}}) {
				return
			}
		}
		sb.Reset() // 新一步的文本从零累计；只有最终无工具调用步的文本进入锚点
	}
	// 步数耗尽：以 EndError 收束（不写锚点——轮次未完成，已有 delta 留在账本）
	emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("step limit reached (%d steps)", MaxStepsPerTurn)})
}

// consumeStream 消费一步的流：增量落账本并上抛，累计工具调用分片。
// 返回：本步文本 / 累计完成的工具调用 / 终态块 / done。
// done 语义：false = EndDone 正常完成（调用方按 calls 分派：0→锚点收尾，>0→工具续步）；
// true = 非 EndDone 终态或端口契约破坏（调用方透传 terminal 后终止整轮）。
// 为什么不用"abort"一个标志包打：正常完成与异常终止必须可区分，否则终态被丢弃。
func (l *Loop) consumeStream(ctx context.Context, ledger *session.Ledger, ch <-chan llm.StreamChunk, out chan llm.StreamChunk, sb *strings.Builder) (string, []llm.ToolCall, llm.StreamChunk, bool) {
	acc := newCallAccumulator()
	for chunk := range ch {
		if chunk.EndReason != llm.EndNone {
			if chunk.EndReason != llm.EndDone {
				return sb.String(), acc.list(), chunk, true
			}
			return sb.String(), acc.list(), chunk, false
		}
		if len(chunk.ToolCalls) > 0 {
			acc.merge(chunk.ToolCalls)
			continue // 工具调用分片是协议细节，不上抛 UI
		}
		if chunk.Delta != "" || chunk.Thinking != "" {
			// 增量 write-ahead：先落账本再上抛——取消/崩溃时已产生内容不丢（C-APP-2）
			if _, err := ledger.Append(session.EventAssistantDelta, map[string]any{
				"text":     chunk.Delta,
				"thinking": chunk.Thinking,
			}); err != nil {
				return "", nil, llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist assistant delta: %w", err)}, true
			}
			sb.WriteString(chunk.Delta)
		}
		select {
		case out <- chunk:
		case <-ctx.Done():
			// 消费方离开：已产生事件均已落账本（C-APP-2）
			return "", nil, llm.StreamChunk{EndReason: llm.EndCancelled, Err: ctx.Err()}, true
		}
	}
	// 端口契约被破坏（通道无终态即关闭）：兜底 EndError，绝不静默结束
	return "", nil, llm.StreamChunk{EndReason: llm.EndError, Err: errors.New("runtime closed without terminal")}, true
}

// execTool 执行单个工具调用；任何机制失败都转为模型可见的业务失败。
func (l *Loop) execTool(ctx context.Context, call llm.ToolCall) tools.ToolResult {
	if l.registry == nil {
		return tools.ToolResult{Content: "no tools available", IsError: true}
	}
	t, ok := l.registry.Get(call.Name)
	if !ok {
		return tools.ToolResult{Content: fmt.Sprintf("unknown tool %q", call.Name), IsError: true}
	}
	res, err := t.Execute(ctx, json.RawMessage(call.Arguments))
	if err != nil {
		return tools.ToolResult{Content: fmt.Sprintf("tool %q mechanism error: %v", call.Name, err), IsError: true}
	}
	return res
}

// callAccumulator 按分片 Index 累计流式工具调用（跨分片拼接 arguments）。
type callAccumulator struct {
	ordered []*llm.ToolCall
	byIndex map[int]*llm.ToolCall
}

func newCallAccumulator() *callAccumulator {
	return &callAccumulator{byIndex: make(map[int]*llm.ToolCall)}
}

func (a *callAccumulator) merge(deltas []llm.ToolCallChunk) {
	for _, d := range deltas {
		c, ok := a.byIndex[d.Index]
		if !ok {
			c = &llm.ToolCall{}
			a.byIndex[d.Index] = c
			a.ordered = append(a.ordered, c)
		}
		if c.ID == "" {
			c.ID = d.ID
		}
		if c.Name == "" {
			c.Name = d.Name
		}
		c.Arguments += d.ArgumentsDelta
	}
}

func (a *callAccumulator) list() []llm.ToolCall {
	if len(a.ordered) == 0 {
		return nil
	}
	out := make([]llm.ToolCall, 0, len(a.ordered))
	for _, c := range a.ordered {
		out = append(out, *c)
	}
	return out
}
