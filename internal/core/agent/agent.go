// Package agent 是 ReAct 执行内核：LLM 生成 → 工具调用 → 结果回填 → 续步。
//
// 做什么：无状态的对话执行循环。会话状态由 core/session 账本持有（write-ahead：
// 事件先落账本再上抛 UI），模型访问经 core/llm.ChatRuntime，工具执行经 core/tools
// 端口（M3/M4 接入）——本包自身不做任何直接外部访问。
// 被谁依赖：internal/app（编排）。
// 依赖谁：core/llm、core/tools、core/session（端口与类型）。
//
// 边界铁律：Loop 不持有会话文件句柄、不发起 HTTP 请求、不直接执行工具；
// Phase 状态机保证单轮互斥（防并发轮次撕裂账本语义，ADR-0005 State 模式）。
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
)

// MaxStepsPerTurn 单轮（一次用户消息触发的连续推理）最大步数。
// 为什么 25：足够覆盖常见多步编码任务，同时封顶失控循环的 token 消耗；
// M2 为单步流式环（无工具），M3/M4 引入 ToolPort 后启用多步 ReAct。
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
	runtime llm.ChatRuntime
	model   string
	phase   atomic.Int32
}

// NewLoop 构造循环：构造期注入运行时与模型名（依赖不可变，禁止导出可变字段）。
func NewLoop(rt llm.ChatRuntime, model string) *Loop {
	return &Loop{runtime: rt, model: model}
}

// Phase 返回当前轮次状态。
func (l *Loop) Phase() Phase { return Phase(l.phase.Load()) }

// Run 执行一轮对话：
//  1. 用户消息落账本（失败同步上抛，C-APP-1）；
//  2. 从账本重放推导多轮消息（会话恢复的核心路径）；
//  3. 经 ChatRuntime 发起流式调用，增量先落账本再上抛 UI；
//  4. 正常完成时写入 assistant_message 锚点 + turn_end。
//
// 返回的通道恰好含一个 EndReason != EndNone 终态块后关闭。
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
	ch, err := l.runtime.Chat(ctx, llm.ChatRequest{Model: l.model, Messages: msgs}, llm.DefaultRuntimePolicy())
	if err != nil {
		// 前置失败（连接/预算）：同步上抛，由编排层决定是否提示重试
		l.phase.Store(int32(PhaseIdle))
		return nil, err
	}
	out := make(chan llm.StreamChunk)
	go l.turn(ctx, ledger, ch, out)
	return out, nil
}

// deriveMessages 从账本重放推导多轮消息。
// 只取 user_message / assistant_message 两类锚点：delta 是过程量，assistant_message
// 才是"确认完成"的消息；被取消的轮次天然不进入历史（其 delta 仍留在账本供审计）。
func (l *Loop) deriveMessages(ledger *session.Ledger) ([]llm.Message, error) {
	var msgs []llm.Message
	err := ledger.Replay(func(ev session.SessionEvent) error {
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

// turn 是单轮的消费循环：增量落账本 → 上抛；终态透传（EndDone 前先写锚点）。
func (l *Loop) turn(ctx context.Context, ledger *session.Ledger, ch <-chan llm.StreamChunk, out chan llm.StreamChunk) {
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

	var sb strings.Builder // 已确认落盘的助手文本（不含被取消的尾部）
	for chunk := range ch {
		if chunk.EndReason != llm.EndNone {
			if chunk.EndReason == llm.EndDone {
				// 锚点 write-ahead：先落盘再上抛；失败则以 EndError 取代 EndDone（恰好一个终态）
				if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": sb.String()}); err != nil {
					emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist assistant message: %w", err)})
					return
				}
				if _, err := ledger.Append(session.EventTurnEnd, map[string]string{"reason": "done"}); err != nil {
					emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist turn end: %w", err)})
					return
				}
			}
			emitTerminal(chunk)
			return
		}
		if chunk.Delta != "" || chunk.Thinking != "" {
			// 增量 write-ahead：先落账本再上抛——取消/崩溃时已产生内容不丢（C-APP-2）
			if _, err := ledger.Append(session.EventAssistantDelta, map[string]any{
				"text":     chunk.Delta,
				"thinking": chunk.Thinking,
			}); err != nil {
				emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist assistant delta: %w", err)})
				return
			}
			sb.WriteString(chunk.Delta)
		}
		select {
		case out <- chunk:
		case <-ctx.Done():
			// 消费方离开：已产生事件均已落账本（C-APP-2），终态由消费方弃读
			return
		}
	}
	// 端口契约被破坏（通道无终态即关闭）：兜底 EndError，绝不静默结束
	emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: errors.New("runtime closed without terminal")})
}
