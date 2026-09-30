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
	"sync"
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
	// asker 为 nil 表示问答通道未启用（ask_user 收到引导性结果）；经 SetAsker 注入
	asker Asker
	// preface 是每轮前置的系统说明（技能与 MCP 清单）。不写入账本，随配置变化。
	preface string
	// prefaceFn 是动态 preface（0.2.33）：每个执行步骤前实时取一次——扩展在
	// 回合内被 ManageTool 增删后，模型在后续步骤立即看到最新清单，不必等下一轮。
	// 设置了 prefaceFn 时每步覆盖静态 preface。
	prefaceFn func() string
	// forced 是本轮"开口前必须先调用"的工具（第 7 批：输入框里指定的技能 / MCP 工具）。
	// nil = 不强制——默认路径与旧版完全一致。
	forced *ForcedTool
	// trailingNote 返回"只存在于本次请求"的补充说明（第 8 批：工作区检查结果）。
	// nil / 返回空串 = 不附。
	trailingNote func() string
	// ctxBudgetTokens 是本轮的上下文预算（token；0 = 未配置上限）。
	// 由装配层从渠道配置折算注入（第 2 批）：派生历史时按预算分级折叠旧内容。
	ctxBudgetTokens int
	// prefaceSkips 统计"preface 与上一手相同而跳过替换"的次数（第 5 批；
	// 测试观测点——跳过即保持 system 前缀逐字节不变，prompt cache 不失效）。
	prefaceSkips int
	// WIP(todoTrack)：清单过期提醒未完成（todoTrack 类型未定义），暂注释待补全后恢复：
	// todo 是本轮任务清单的进度（不进账本）。模型经常只提交一次快照，
	// 随后干活却不再回写，界面就停在「全部未完成」。这里记下上次快照，
	// 快照过期时在下一步提醒它重交——提醒只存在于本轮内存消息里。
	// todo todoTrack
}

// NewLoop 构造循环：构造期注入运行时、模型与工具注册表（nil = 无工具）。
func NewLoop(rt llm.ChatRuntime, model string, registry *tools.Registry) *Loop {
	return &Loop{runtime: rt, model: model, registry: registry}
}

// SetPreface 设置每轮对话开头的系统说明。空串表示不插入。
func (l *Loop) SetPreface(text string) {
	if l == nil {
		return
	}
	l.preface = text
}

// SetPrefaceFn 设置动态系统说明：每个执行步骤前实时取值（0.2.33）。
// 为什么需要：扩展自管理让模型在回合内增删技能/MCP——静态 preface 在回合
// 开始就固定，模型添加后说"当前没有技能"、后续步骤也用不上。动态取值让
// 清单每步刷新，添加当回合即可用。
func (l *Loop) SetPrefaceFn(fn func() string) {
	if l == nil {
		return
	}
	l.prefaceFn = fn
}

// Preface 返回当前生效的系统说明（动态优先；测试与诊断用）。
func (l *Loop) Preface() string {
	if l == nil {
		return ""
	}
	if l.prefaceFn != nil {
		return l.prefaceFn()
	}
	return l.preface
}

// SetContextBudget 设置本轮的上下文预算（token；0 = 未配置上限，不裁剪）。
// 第 2 批：装配层从渠道配置（contextLimit）折算注入，派生历史时按预算分级折叠。
func (l *Loop) SetContextBudget(tokens int) {
	if l == nil {
		return
	}
	if tokens < 0 {
		tokens = 0
	}
	l.ctxBudgetTokens = tokens
}

// attachPreface 把静态系统说明放到最前（派生结果本身不含 system；动态 preface
// 由 applyDynamicPreface 每步覆盖）。Run 与"续跑前重新折叠"共用同一复原逻辑。
func (l *Loop) attachPreface(msgs []llm.Message) []llm.Message {
	text := strings.TrimSpace(l.preface)
	if text == "" {
		return msgs
	}
	return append([]llm.Message{{Role: "system", Content: text}}, msgs...)
}

// contextEvent 把派生读数封成上报块（油表：预算 / 估算 / 折叠项 / 超限标记）。
// 预算未配置且没有折叠时返回 nil——保持旧行为零噪声（不新增事件）。
func contextEvent(info DeriveInfo) *llm.ContextEvent {
	if info.BudgetTokens <= 0 && info.FoldedImages+info.FoldedTools+info.FoldedReads == 0 {
		return nil
	}
	return &llm.ContextEvent{
		EstimatedTokens: info.EstimatedTokens,
		BudgetTokens:    info.BudgetTokens,
		FoldedImages:    info.FoldedImages,
		FoldedTools:     info.FoldedTools,
		FoldedReads:     info.FoldedReads,
		Dropped:         info.Dropped,
	}
}

// ForcedTool 是用户在输入框里指定的"本轮必须用上"的工具（第 7 批；第 8 批细分两种语义）：
//
//	skill —— 程序代替模型发起这一次调用（技能正文是喂料，不需要模型先开口）；
//	mcp   —— **钉住**：程序不替模型发，而是在本次请求里附一句"第一笔调用必须是这台
//	         服务器的这个工具"的约束，参数由模型按工具说明填（用户不再填参数 JSON）。
//	         server/tool 被模型改掉，本轮就停在这条错误上——不执行被改过的调用，
//	         也不继续空答（否则用户以为在用它指定的工具，实际不是）。
type ForcedTool struct {
	Name      string // 只允许 skill / mcp（见 forcedToolAllowed）
	Arguments string // skill：程序直接发起时原样传下去的 JSON（空 = {}）
	Server    string // mcp：钉住的服务器名（模型不许改）
	Tool      string // mcp：钉住的工具名（模型不许改）
}

// pinnedMCP 报告这是否是"钉住一笔 MCP 调用"的形态（第 8 批）：指定了 server/tool
// 且**没有人手写参数**。带参数的旧消息（第 7 批遗留）仍走"程序代发、原样传入"。
func (f *ForcedTool) pinnedMCP() bool {
	return f != nil && f.Name == "mcp" &&
		strings.TrimSpace(f.Server) != "" && strings.TrimSpace(f.Tool) != "" &&
		strings.TrimSpace(f.Arguments) == ""
}

// callArguments 是"程序代发"时原样传给工具的 JSON。
// skill：用户/内核给的参数；mcp（旧消息带参数）：server/tool 以钉住值为准，
// 用户手写的那份参数原样嵌进 arguments（第 8 批：新界面不再产生这种消息）。
func (f *ForcedTool) callArguments() string {
	if f == nil {
		return "{}"
	}
	if f.Name == "mcp" {
		inner := strings.TrimSpace(f.Arguments)
		if inner == "" || inner == "null" {
			inner = "{}"
		}
		b, err := json.Marshal(map[string]any{
			"server": f.Server, "tool": f.Tool, "arguments": json.RawMessage(inner),
		})
		if err != nil {
			return ""
		}
		return string(b)
	}
	args := strings.TrimSpace(f.Arguments)
	if args == "" {
		args = "{}"
	}
	return args
}

// pinnedCallNote 是钉住 MCP 调用时附在请求末尾的说明（第 8 批）。
// 只存在于本次请求：不写账本、不进系统提示——系统提示必须在技能清单与语气设置
// 不变时逐字相同，往那里塞逐轮变化的内容会打断上游 prompt cache。
func pinnedCallNote(server, tool string) string {
	return fmt.Sprintf("（本轮约束，不是新问题）请把第一笔工具调用固定为 mcp：server=%q、tool=%q，"+
		"参数按该工具的说明填写。不要先调用别的工具，也不要改用其它 server 或 tool。",
		server, tool)
}

// pinnedCallOK 校验模型本轮的第一笔工具调用是否就是钉住的那一笔（第 8 批）。
// 返回空串表示通过；否则返回可读原因。
func pinnedCallOK(ft *ForcedTool, calls []llm.ToolCall) string {
	if len(calls) == 0 {
		return fmt.Sprintf("你没有调用工具，选择了直接回答；本轮要求的第一笔调用是 mcp：server=%s、tool=%s",
			ft.Server, ft.Tool)
	}
	first := calls[0]
	if first.Name != "mcp" {
		return fmt.Sprintf("第一笔调用是 %s，而不是 mcp：server=%s、tool=%s", first.Name, ft.Server, ft.Tool)
	}
	var p struct {
		Server string `json:"server"`
		Tool   string `json:"tool"`
	}
	if err := json.Unmarshal([]byte(first.Arguments), &p); err != nil {
		return fmt.Sprintf("mcp 调用的参数不是合法 JSON（%v）；本轮要求 server=%s、tool=%s", err, ft.Server, ft.Tool)
	}
	if strings.TrimSpace(p.Server) != ft.Server || strings.TrimSpace(p.Tool) != ft.Tool {
		return fmt.Sprintf("你把调用改成了 server=%s、tool=%s；本轮要求的是 server=%s、tool=%s",
			strings.TrimSpace(p.Server), strings.TrimSpace(p.Tool), ft.Server, ft.Tool)
	}
	return ""
}

// SetForcedTool 注入本轮的强制工具（nil = 不强制，行为与旧版完全一致）。
func (l *Loop) SetForcedTool(ft *ForcedTool) {
	if l == nil {
		return
	}
	l.forced = ft
}

// SetTrailingNote 注入"只存在于本次请求"的补充说明（第 8 批：工作区检查结果）。
// 它在真正的用户消息之后另附一条 user 消息——**不写账本、不进系统提示**
// （系统提示必须在技能清单与语气设置不变时逐字相同，往那里塞逐轮变化的内容
// 会打断上游 prompt cache）。返回空串 = 什么都不附。
func (l *Loop) SetTrailingNote(fn func() string) {
	if l == nil {
		return
	}
	l.trailingNote = fn
}

// forcedToolAllowed 白名单：只有"给模型喂料"的工具可以被强制（skill 取技能正文、
// mcp 转发 MCP 工具输出）。fs/shell 这类能改磁盘、能执行命令的绝不在这里放行——
// 强制调用不能变成"绕过模型与审批直接执行工具"的口子。
func forcedToolAllowed(name string) bool { return name == "skill" || name == "mcp" }

// applyDynamicPreface 在每个执行步骤前刷新系统说明（prefaceFn 优先于静态）。
// 第 5 批：文本与上一手完全相同就不触碰 system——逐字节不变的前缀是上游
// prompt cache 命中的前提，无变化的覆盖是纯噪声（也避免无谓改写消息数组）。
func (l *Loop) applyDynamicPreface(msgs []llm.Message) []llm.Message {
	if l.prefaceFn == nil {
		return msgs
	}
	text := strings.TrimSpace(l.prefaceFn())
	if text == "" {
		return msgs // 取值失败/为空：保持现有 system，不打断回合
	}
	if len(msgs) > 0 && msgs[0].Role == "system" {
		if msgs[0].Content == text {
			l.prefaceSkips++ // 清单没变：跳过替换（测试观测点）
			return msgs
		}
		msgs[0].Content = text
		return msgs
	}
	return append([]llm.Message{{Role: "system", Content: text}}, msgs...)
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
func (l *Loop) Run(ctx context.Context, ledger *session.Ledger, userText string, userAtts ...session.UserAttachment) (<-chan llm.StreamChunk, error) {
	if !l.phase.CompareAndSwap(int32(PhaseIdle), int32(PhaseRunning)) {
		return nil, ErrBusy
	}
	// 用户消息 write-ahead：持久化成功才允许继续（账本即事实源）。
	// 0.0.10：payload 携带附件引用（图片相对路径/文件路径与内联标记）。
	userPayload := map[string]any{"text": userText}
	if len(userAtts) > 0 {
		userPayload["attachments"] = userAtts
	}
	if _, err := ledger.Append(session.EventUserMessage, userPayload); err != nil {
		l.phase.Store(int32(PhaseIdle))
		return nil, fmt.Errorf("persist user message: %w", err)
	}
	msgs, ctxInfo, err := deriveMessagesWith(ledger, DeriveOptions{BudgetTokens: l.ctxBudgetTokens})
	if err != nil {
		l.phase.Store(int32(PhaseIdle))
		return nil, fmt.Errorf("derive history: %w", err)
	}
	// 折完仍超预算：不发请求（0.0.11）。发出去必然被上游按上下文长度拒绝，还会把
	// "本地预算不够"伪装成上游错误；这里给明确终态，说清差在哪、能做什么。
	// 用户原话已 write-ahead 落账本（上面几行），不会因这一判断丢失。
	if ctxInfo.Dropped {
		l.phase.Store(int32(PhaseIdle))
		msg := fmt.Sprintf("本轮上下文（估算约 %d tok）超过渠道上限（%d tok）：折叠旧内容后仍装不下。"+
			"可以调大该渠道的上下文上限、换一条上限更大的渠道，或新开一轮对话。",
			ctxInfo.EstimatedTokens, ctxInfo.BudgetTokens)
		if _, aerr := ledger.Append(session.EventError, map[string]any{"message": msg}); aerr != nil {
			return nil, fmt.Errorf("persist context overflow: %w", aerr)
		}
		return nil, errors.New(msg)
	}
	msgs = l.attachPreface(msgs)
	// 仅本次请求的补充说明（第 8 批）：附在真正的用户消息之后——不写账本，
	// 也不动系统提示（逐轮变化的文本进系统提示会打断上游 prompt cache）。
	if l.trailingNote != nil {
		if note := strings.TrimSpace(l.trailingNote()); note != "" {
			msgs = append(msgs, llm.Message{Role: "user", Content: note})
		}
	}
	var toolDefs []llm.ToolDef
	if l.registry != nil {
		toolDefs = l.registry.Definitions()
	}
	out := make(chan llm.StreamChunk)
	go l.turn(ctx, ledger, msgs, toolDefs, out, ctxInfo)
	return out, nil
}

// turn 是多步消费循环：每步一次模型调用；工具调用触发续步。
func (l *Loop) turn(ctx context.Context, ledger *session.Ledger, msgs []llm.Message, toolDefs []llm.ToolDef, out chan llm.StreamChunk, ctxInfo DeriveInfo) {
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
		// 终态兜底刷盘（第 1 批合批）：取消/错误路径没有"关键事件"来冲掉攒批的
		// 增量——用户中断后进程被杀时，已产生的部分输出绝不能丢（C-APP-2）。
		// 刷盘失败是硬错误：覆盖为 EndError 上抛（账本写不进去，本轮不算完成）。
		if err := ledger.Flush(); err != nil {
			c = llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("flush ledger: %w", err)}
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

	// 工具调用落账 + 终态卡（0.0.06）：提到步循环外定义——第 7 批的"强制工具"要在模型
	// 开口前先跑一次，走的是同一套落账/上报路径（结果形态与模型自发调用完全一致）。
	finishCall := func(call llm.ToolCall, result tools.ToolResult) error {
		// 账本 payload：模型可见字段之外，一并落 UI 专用数据——语义标签/diff
		// （0.0.06）与撤销快照（0.0.07）。undo 携带旧全文：重启后仍能"恢复
		// 写入前"；它只被后端恢复接口读取，derive 投影不解析（不进模型上下文）。
		payload := map[string]any{
			"id": call.ID, "name": call.Name, "content": result.Content, "is_error": result.IsError,
			"title": result.Title, "op": result.Op, "diff": result.Diff,
		}
		if result.Undo != nil {
			payload["undo"] = map[string]any{
				"path": result.Undo.Path, "old_exists": result.Undo.OldExists,
				"old_content": result.Undo.OldContent, "new_sha256": result.Undo.NewSHA256,
			}
		}
		if result.UndoNote != "" {
			payload["undo_note"] = result.UndoNote
		}
		if _, err := ledger.Append(session.EventToolResult, payload); err != nil {
			return err
		}
		// OpenAI 协议：assistant(tool_calls) 之后必须回填 role=tool 结果消息，
		// 下一续步请求才合法（结果经 ToolCallID 与调用配对）
		msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: call.ID, Content: result.Content})
		status := "success"
		if result.IsError {
			status = "error"
		}
		content := result.Content
		if len(content) > toolEventIPCLimit {
			// 0.0.06 头尾保留：UI 完整视图同样需要尾部（失败汇总在末尾）
			content = tools.HeadTail(content, toolEventIPCLimit)
		}
		summary := result.Content
		if len(summary) > 200 {
			// 走 truncateToBytes：按字节切会把中文切成非法 UTF-8（0.2.35 审计#8）
			summary = truncateToBytes(summary, 200) + "…"
		}
		undoPath, undoExists := "", false
		if result.Undo != nil {
			undoPath, undoExists = result.Undo.Path, result.Undo.OldExists
		}
		if forward(llm.StreamChunk{ToolEvent: &llm.ToolEvent{
			Name: call.Name, Status: status, Summary: summary, Content: content, Diff: result.Diff,
			Title: result.Title, Op: result.Op, CallID: call.ID,
			HasUndo: result.Undo != nil, UndoPath: undoPath, UndoExists: undoExists, UndoNote: result.UndoNote,
		}}) {
			return errConsumerGone
		}
		return nil
	}
	emitCallStart := func(call llm.ToolCall) error {
		if _, err := ledger.Append(session.EventToolCall, map[string]string{
			"id": call.ID, "name": call.Name, "arguments": call.Arguments,
		}); err != nil {
			return err
		}
		// running 事件（0.0.06）：执行前先上抛"进行中"动态（只进实时流，
		// 不落账本——账本只有终态结果）。前端据此先出"执行中"卡并随
		// 终态事件原地更新（CallID 配对），卡片输出随事件增长。
		forward(llm.StreamChunk{ToolEvent: &llm.ToolEvent{
			Name: call.Name, Status: "running", Summary: "执行中…", CallID: call.ID,
		}})
		return nil
	}

	// 上下文治理读数（第 2 批）：每轮一次，界面油表显示预算/估算/折叠标记——
	// 折叠绝不静默（预算已配置或发生了折叠才上报，保持旧行为零噪声）。
	if ev := contextEvent(ctxInfo); ev != nil {
		if forward(llm.StreamChunk{Context: ev}) {
			return
		}
	}

	// 强制工具（第 7 批）：用户在输入框里指定了技能或 MCP 工具——本轮**开口前**先调它。
	// 参数由调用方给定（服务器名/工具名模型改不了），结果按普通工具调用进账本与上下文，
	// 前端显示成一张普通工具卡。失败就停在这条工具错误上：不假装用过，也不让模型空答。
	if l.forced != nil && !forcedToolAllowed(l.forced.Name) {
		emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf(
			"不支持的强制工具：%s（只支持 skill / mcp）", l.forced.Name)})
		return
	}
	// 钉住 MCP 调用（第 8 批）：程序不发这笔调用，只附一句**仅本次请求**的约束，
	// 让模型带着自己的参数去调；下一段循环里校验它的第一笔调用（pinnedCallOK）。
	pinPending := false
	if l.forced.pinnedMCP() {
		msgs = append(msgs, llm.Message{Role: "user", Content: pinnedCallNote(l.forced.Server, l.forced.Tool)})
		pinPending = true
	}
	// 强制工具（第 7 批，skill 与"带参数的旧 mcp 消息"）：本轮**开口前**先调它。
	// 参数由调用方给定；结果按普通工具调用进账本与上下文，前端显示成一张普通工具卡。
	// 失败就停在这条工具错误上：不假装用过，也不让模型空答。
	if l.forced != nil && !pinPending {
		args := l.forced.callArguments()
		call := llm.ToolCall{ID: fmt.Sprintf("call-%d", ledger.NextSeq()), Name: l.forced.Name, Arguments: args}
		if err := emitCallStart(call); err != nil {
			emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist tool call: %w", err)})
			return
		}
		result := l.dispatchTool(ctx, call, ledger, forward)
		if err := finishCall(call, result); err != nil {
			if errors.Is(err, errConsumerGone) {
				if ctx.Err() != nil {
					emitTerminal(llm.StreamChunk{EndReason: llm.EndCancelled, Err: ctx.Err()})
				}
				return
			}
			emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("finish tool result: %w", err)})
			return
		}
		if result.IsError {
			detail := strings.TrimSpace(result.Content)
			if detail == "" {
				detail = "工具执行失败"
			}
			emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf(
				"指定的 %s 不可用：%s", l.forced.Name, detail)})
			return
		}
		if ctx.Err() != nil {
			emitTerminal(llm.StreamChunk{EndReason: llm.EndCancelled, Err: ctx.Err()})
			return
		}
		// 结果进本轮上下文：与模型自发调用**同一形态**（assistant(tool_calls) + role=tool）
		// ——直接从账本重新派生，不手搓消息序列（配对与折叠规则与后续步骤同源）。
		refreshed, next, derr := deriveMessagesWith(ledger, DeriveOptions{BudgetTokens: l.ctxBudgetTokens})
		if derr != nil {
			emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("derive history: %w", derr)})
			return
		}
		msgs = l.attachPreface(refreshed)
		if ev := contextEvent(next); ev != nil {
			if forward(llm.StreamChunk{Context: ev}) {
				return
			}
		}
	}

	var sb strings.Builder // 当前步已确认落盘的助手文本
	// 步数分段（第 3 批）：默认每段 MaxStepsPerTurn 步；一段用尽先询问用户是否
	// 续跑（见循环末尾），同意则重新计段，拒绝/取消/无问答通道走收尾。
	segment := 1
	for step := 1; step <= MaxStepsPerTurn; step++ {
		if err := ctx.Err(); err != nil {
			emitTerminal(llm.StreamChunk{EndReason: llm.EndCancelled, Err: err})
			return
		}
		msgs = l.applyDynamicPreface(msgs) // 每步刷新：扩展回合内增删立即生效
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
		// 钉住的第一笔调用（第 8 批）：只校验本轮的**第一次**模型响应（之后模型可以
		// 自由调用别的工具继续干活）。不通过就停在这里：不执行被改过的调用、不写锚点
		// （避免留下孤儿 tool_call），也不进入"最终回答"分支——不能让模型绕开它空答。
		if pinPending {
			pinPending = false
			if reason := pinnedCallOK(l.forced, calls); reason != "" {
				msg := fmt.Sprintf("指定的 MCP 工具没有被调用：%s。本轮已停止，未被执行的调用没有落账。", reason)
				if _, aerr := ledger.Append(session.EventError, map[string]any{"message": msg}); aerr != nil {
					emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist pinned call violation: %w", aerr)})
					return
				}
				emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: errors.New(msg)})
				return
			}
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

		// 工具调用轮：按索引补全空 ID（写入 calls 与 assistant.ToolCalls 共享底层），再落盘执行
		// 0.0.06：本步若已有确认的助手正文（模型调用工具前说了话），先落
		// EventAssistantMsg 锚点——下一轮 deriveMessages 把它并入
		// assistant(text, tool_calls)，跨轮历史不再丢"为什么调工具"。
		// EventAssistantDelta 半截内容仍然不投影（锚点 = 已确认完成的文本）。
		if text != "" {
			if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": text}); err != nil {
				emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist assistant message: %w", err)})
				return
			}
		}
		msgs = append(msgs, llm.Message{Role: "assistant", Content: text, ToolCalls: calls})
		// 0.0.09：只读工具并行圈速——好模型一次要读五个文件、搜两处，串行
		// 每本都要等上一本读完再请求下一次模型，圈速浪费在等不在模型。
		// 分段规则：**连续**的只读调用为一组并发执行；写类（write/replace/shell/
		// ext_manage 及一切不确定的）单独串行——两个人同时改同一个文件绝不允许。
		// 账本事件与 msgs 顺序始终按 calls 原序（并发只在执行本身，落账在收集后
		// 按序做——Ledger.Append 内部有锁但这里根本不需要并发写）。
		for i := range calls {
			if calls[i].ID == "" {
				calls[i].ID = fmt.Sprintf("call-%d", ledger.NextSeq())
			}
		}
		i := 0
		for i < len(calls) {
			if !isReadOnlyCall(calls[i]) {
				call := calls[i]
				i++
				if err := emitCallStart(call); err != nil {
					emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist tool call: %w", err)})
					return
				}
				if err := finishCall(call, l.dispatchTool(ctx, call, ledger, forward)); err != nil {
					if errors.Is(err, errConsumerGone) {
						// 消费方离开：事件已落账本。但若是"用户停止"引发的取消，
						// 必须补发终态（cancel 后 forward 的 select 可能随机走
						// ctx.Done 分支把终态事件当"消费方离开"吞掉——0.0.10 修复）。
						if ctx.Err() != nil {
							emitTerminal(llm.StreamChunk{EndReason: llm.EndCancelled, Err: ctx.Err()})
						}
						return
					}
					emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("finish tool result: %w", err)})
					return
				}
				// 停止打断（0.0.10）：工具执行完立即看取消——被中断的调用之后，
				// 本轮不再执行剩余工具、不再请求模型，终态 EndCancelled。
				// （此前只在下一轮开头看 ctx.Err，人点停止后界面要等命令自己结束。）
				if ctx.Err() != nil {
					emitTerminal(llm.StreamChunk{EndReason: llm.EndCancelled, Err: ctx.Err()})
					return
				}
				continue
			}
			// 只读段：[i, j) 连续只读——并发执行，按序收尾
			j := i
			for j < len(calls) && isReadOnlyCall(calls[j]) {
				j++
			}
			seg := calls[i:j]
			for _, call := range seg {
				if err := emitCallStart(call); err != nil {
					emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist tool call: %w", err)})
					return
				}
			}
			results := make([]tools.ToolResult, len(seg))
			var wg sync.WaitGroup
			for k := range seg {
				wg.Add(1)
				go func(k int, call llm.ToolCall) {
					defer wg.Done()
					results[k] = l.dispatchTool(ctx, call, ledger, forward)
				}(k, seg[k])
			}
			wg.Wait()
			for k := range seg {
				if err := finishCall(seg[k], results[k]); err != nil {
					if errors.Is(err, errConsumerGone) {
						// 消费方离开 + 用户停止：补发终态（同上）
						if ctx.Err() != nil {
							emitTerminal(llm.StreamChunk{EndReason: llm.EndCancelled, Err: ctx.Err()})
						}
						return
					}
					emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("finish tool result: %w", err)})
					return
				}
			}
			// 停止打断（0.0.10）：并行段收尾后同样立即检查（并行段内工具已感知
			// 取消快速返回；这里保证不再进入后续段/模型请求）
			if ctx.Err() != nil {
				emitTerminal(llm.StreamChunk{EndReason: llm.EndCancelled, Err: ctx.Err()})
				return
			}
			i = j
		}
		// WIP(todoTrack)：清单过期提醒调用（待类型补全后恢复）
		// // 本步干过活且清单还是旧快照：下一步先让模型重交，再写最终答复。
		// // 同样不进账本（理由见收尾处的提醒）。
		// if msg, ok := l.todo.consumeRefresh(false); ok {
		// 	msgs = append(msgs, llm.Message{Role: "user", Content: msg})
		// }
		sb.Reset() // 新一步的文本从零累计；只有最终无工具调用步的文本进入锚点

		// 步数分段（第 3 批）：一段用尽先发一条可取消的询问（复用 ask_user 通道），
		// 用户同意再续跑一段；拒绝/取消/无问答通道则跳出走收尾。
		// 旧行为是不经询问直接打一发"无工具总结"收工——长任务被硬停在 25 步。
		if step >= MaxStepsPerTurn {
			if l.continueAfterLimit(ctx, segment) {
				segment++
				// 续跑前重新折叠（0.0.11）：本段已把大量工具输出写进上下文，开局那次
				// 派生结果已经过时——按当前预算重新派生一次，续跑段不背着满上下文；
				// 油表读数同步刷新（此前续跑后仍显示开局数字，用户以为折叠没生效）。
				refreshed, next, derr := deriveMessagesWith(ledger, DeriveOptions{BudgetTokens: l.ctxBudgetTokens})
				if derr != nil {
					emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("derive history: %w", derr)})
					return
				}
				msgs = l.attachPreface(refreshed)
				if ev := contextEvent(next); ev != nil {
					if forward(llm.StreamChunk{Context: ev}) {
						return
					}
				}
				msgs = append(msgs, llm.Message{Role: "user", Content: fmt.Sprintf(
					"用户同意继续：已跑满 %d 步（第 %d 段开始），现在继续执行，仍可使用全部工具。",
					MaxStepsPerTurn, segment)})
				step = 0 // 重新计一段（for 自增后回到 1）
			} else if ctx.Err() != nil {
				emitTerminal(llm.StreamChunk{EndReason: llm.EndCancelled, Err: ctx.Err()})
				return
			} else {
				break
			}
		}
	}
	// 步数耗尽（0.0.06 改造）：不再直接 EndError——强制一步"无工具总结"，
	// 让模型把已完成/未完成讲清楚后正常收束（锚点照落、EndDone、UI 不报错）。
	// 这一失败（模型调用失败）才走错误终态。上游取消仍由 ctx.Err 捕获。
	msgs = append(msgs, llm.Message{Role: "user", Content: fmt.Sprintf(
		"已达到单轮步数上限（%d 步）。本轮不再提供任何工具。请立即总结：1) 已完成什么；2) 未完成什么；3) 建议的下一步。不要再尝试调用工具。", MaxStepsPerTurn)})
	msgs = l.applyDynamicPreface(msgs)
	ch, err := l.runtime.Chat(ctx, llm.ChatRequest{
		Model:    l.model,
		Messages: msgs,
		Tools:    nil, // 强制空工具集：总结步不可能再执行工具
	}, llm.DefaultRuntimePolicy())
	if err != nil {
		emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("step limit reached (%d steps), summary call failed: %w", MaxStepsPerTurn, err)})
		return
	}
	text, calls, terminal, done := l.consumeStream(ctx, ledger, ch, out, &sb)
	if done {
		// 非 EndDone 终态（错误/取消）或端口契约破坏：透传
		emitTerminal(terminal)
		return
	}
	_ = calls // 无工具定义下协议上不应有 calls；即便出现也按纯文本收束（不执行）
	if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": text}); err != nil {
		emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist assistant message: %w", err)})
		return
	}
	if _, err := ledger.Append(session.EventTurnEnd, map[string]string{"reason": "done"}); err != nil {
		emitTerminal(llm.StreamChunk{EndReason: llm.EndError, Err: fmt.Errorf("persist turn end: %w", err)})
		return
	}
	emitTerminal(terminal)
}

// dispatchTool 工具执行分派：ask_user/todo 由内核拦截（交互语义），其余走审批闸门（默认关，ADR-0007）。
// 0.0.07：实现 ProgressSink 的工具（shell）在执行中把已捕获输出推给界面——
// forward 携带同一 CallID 的 running 事件，前端更新同一张卡（不新增）。
func (l *Loop) dispatchTool(ctx context.Context, call llm.ToolCall, ledger *session.Ledger, forward func(llm.StreamChunk) bool) tools.ToolResult {
	if call.Name == askToolName {
		return l.runAsk(ctx, call)
	}
	if call.Name == todoToolName {
		return runTodo(call, ledger, forward)
	}
	if l.registry != nil {
		if t, ok := l.registry.Get(call.Name); ok {
			if ps, isSink := t.(tools.ProgressSink); isSink {
				ps.SetProgress(func(partial string) {
					_ = forward(llm.StreamChunk{ToolEvent: &llm.ToolEvent{
						Name: call.Name, Status: "running", Summary: "执行中…",
						Content: partial, CallID: call.ID,
					}})
				})
				defer ps.SetProgress(nil) // 换步/结束必须清除，绝不向下一张卡泄过程
			}
		}
	}
	return l.execToolWithApproval(ctx, call)
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

// errConsumerGone：finishCall 里 forward 返回 true（消费方离开）时的哨兵——
// 调用方据此退出循环（事件已落账本，不需要再 emitTerminal）。
var errConsumerGone = errors.New("consumer gone")

// isReadOnlyCall 判定一次工具调用是否只读（0.0.09 并行白名单）。
// 白名单宁可窄：write/replace/shell/ext_manage 及一切解析不出的形态一律串行——
// 两个人同时改同一个文件的代价远大于少并行几次。
//   - fs：action ∈ {read,list,tree} 才只读（fs 是"一个名字两种人"，按参数分）；
//   - search/git：整体只读（git 在本仓库只有 status/diff/log，无改写子命令——
//     审计约束见工具描述与 ADR；新增子命令时必须回来更新这里）。
func isReadOnlyCall(call llm.ToolCall) bool {
	switch call.Name {
	case "search", "git":
		return true
	case "mcp":
		// MCP（0.0.11）：只有 tool=list 是纯查询（拉服务器工具清单）；其余一律串行——
		// server 侧工具名对我们是黑盒，可能是写操作，"两个人同时动同一资源"的代价
		// 远大于少并行几次（白名单宁可窄，与 fs/search/git 同一纪律）。
		var p struct {
			Tool string `json:"tool"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &p); err != nil {
			return false
		}
		return strings.EqualFold(strings.TrimSpace(p.Tool), "list")
	case "fs":
		var p struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal([]byte(call.Arguments), &p); err != nil {
			return false // 解析不出 = 串行
		}
		switch p.Action {
		case "read", "list", "tree":
			return true
		default:
			return false
		}
	default:
		return false
	}
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
