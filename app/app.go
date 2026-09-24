// Package app 是 Wails 绑定适配层（壳）：把前端 IPC 调用转发给 internal/app 用例层。
// 只做参数转发、类型适配与事件桥接；禁止业务规则（docs/STANDARDS.md 分层表）。
package app

import (
	"context"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"tiancode/internal/app"
	"tiancode/internal/core/llm"
)

// Bind 是暴露给前端（Wails Bind）的入口对象。
// 公开方法 = IPC 端点：一进一出、错误上抛；流式内容经事件桥推送（Observer，ADR-0005）。
//
// 铁律：绑定方法**不得**声明 context.Context 参数。
// 本版本 Wails 的 boundMethod.ParseArgs 要求 JS 实参个数严格等于 Go 声明参数个数，
// 且 Call 直接反射调用（不做任何 ctx 注入）。曾把 ctx 写成首参，
// 导致每次发送都报 "received 2 arguments to method 'app.Bind.Send', expected 3"（实机事故）。
// 应用上下文改由 OnStartup 写入 AppCtx 字段（字段不参与绑定校验）。
type Bind struct {
	chat    *app.ChatService
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	// AppCtx 是 Wails 应用上下文，由 main 的 OnStartup 注入（事件推送与取消传播都依赖它）。
	AppCtx context.Context
}

// New 装配壳层。
func New(chat *app.ChatService) *Bind {
	b := &Bind{chat: chat, cancels: make(map[string]context.CancelFunc)}
	// 审批事件桥（ADR-0007）：内核要"问"时推给前端，前端答复经 ResolveApproval 回流
	chat.SetApprovalHandler(func(e app.ApprovalEvent) {
		wruntime.EventsEmit(b.appCtx(), "chat:approval", map[string]string{
			"id":        e.ID,
			"toolName":  e.ToolName,
			"arguments": e.Arguments,
		})
	})
	return b
}

// appCtx 返回应用上下文；未注入时退化为 Background——
// 宁可事件桥拿到一个无价值的 ctx，也不要 nil panic 打断已建立的数据流。
func (b *Bind) appCtx() context.Context {
	if b.AppCtx == nil {
		return context.Background()
	}
	return b.AppCtx
}

// ListSessions 返回全部会话 ID。
func (b *Bind) ListSessions() ([]string, error) {
	return b.chat.ListSessions()
}

// ExportSessionMarkdown 返回会话的 Markdown 文本（前端负责复制/保存）。
func (b *Bind) ExportSessionMarkdown(sessionID string) (string, error) {
	return b.chat.ExportSessionMarkdown(sessionID)
}

// GetWorkspace 返回当前工作区路径（工具受控根）。
func (b *Bind) GetWorkspace() string { return b.chat.Workspace() }

// SetWorkspace 切换工作区；非法路径（不存在/非目录/空白）返回错误供 UI 展示。
func (b *Bind) SetWorkspace(dir string) error { return b.chat.SetWorkspace(dir) }

// ApprovalPolicy 返回当前需要执行前审批的工具清单（空 = 审批关闭，默认）。
func (b *Bind) ApprovalPolicy() []string { return b.chat.ApprovalPolicy() }

// SetApprovalPolicy 设置需要审批的工具清单；传空数组即关闭审批（ADR-0007 默认关）。
func (b *Bind) SetApprovalPolicy(tools []string) error { return b.chat.SetApprovalPolicy(tools) }

// ResolveApproval 提交用户对某次审批请求的答复（允许/拒绝 + 原因）。
// 未知或已处理的 ID 返回错误——UI 会明确提示，绝不静默放行。
func (b *Bind) ResolveApproval(id string, approved bool, reason string) error {
	return b.chat.ResolveApproval(id, approved, reason)
}

// ListSessionSummaries 返回会话摘要（ID + 用户标题；标题来自账本事件）。
func (b *Bind) ListSessionSummaries() ([]app.SessionSummary, error) {
	return b.chat.SessionSummaries()
}

// RenameSession 重命名会话（标题写入账本，重启后仍可恢复）。
func (b *Bind) RenameSession(sessionID, title string) error {
	return b.chat.RenameSession(sessionID, title)
}

// DeleteSession 删除会话及其账本文件；被删除的会话不再出现在列表。
func (b *Bind) DeleteSession(sessionID string) error {
	return b.chat.DeleteSession(sessionID)
}

// Replay 返回会话的已确认消息（历史恢复）。
func (b *Bind) Replay(sessionID string) ([]app.ChatMessage, error) {
	return b.chat.Replay(sessionID)
}

// Send 发送一条消息；流式内容经事件桥推送：
//   - "chat:chunk"    {sessionID, delta, thinking}
//   - "chat:terminal" {sessionID, endReason, error}
//
// endReason 取值对应 core/llm：1=EndDone 2=EndError 3=EndCancelled 4=EndIdleTimeout。
// 返回值仅在"流建立失败"（前置错误，如配置缺失/连接失败重试耗尽）时非 nil；
// 流中终态一律经 chat:terminal 事件传递。
func (b *Bind) Send(sessionID, text string) error {
	ctx := b.appCtx() // 见 Bind 注释：ctx 不能作绑定方法参数
	runCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.cancels[sessionID] = cancel
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.cancels, sessionID)
		b.mu.Unlock()
	}()

	ch, err := b.chat.Send(runCtx, sessionID, text)
	if err != nil {
		return err
	}

	// 为什么兜底合成终态：极端时序下（取消恰逢发送受阻）上游通道可能无终态关闭，
	// 前端必须始终收到 chat:terminal 才能解锁输入框（C-APP-2 的 UI 侧保证）。
	terminalSeen := false
	emitTerminal := func(reason llm.EndReason, errText string) {
		terminalSeen = true
		wruntime.EventsEmit(ctx, "chat:terminal", map[string]interface{}{
			"sessionID": sessionID,
			"endReason": int(reason),
			"error":     errText,
		})
	}

	for c := range ch {
		if c.Delta != "" || c.Thinking != "" {
			wruntime.EventsEmit(ctx, "chat:chunk", map[string]string{
				"sessionID": sessionID,
				"delta":     c.Delta,
				"thinking":  c.Thinking,
			})
		}
		if c.ToolEvent != nil {
			// 工具卡片数据（M3）：执行动态实时推送，前端渲染独立卡片
			wruntime.EventsEmit(ctx, "chat:tool", map[string]string{
				"sessionID": sessionID,
				"name":      c.ToolEvent.Name,
				"status":    c.ToolEvent.Status,
				"summary":   c.ToolEvent.Summary,
				"diff":      c.ToolEvent.Diff, // 编辑类工具的结构化 diff（无变更时为空串）
			})
		}
		if c.EndReason != llm.EndNone {
			errText := ""
			if c.Err != nil {
				errText = c.Err.Error()
			}
			emitTerminal(c.EndReason, errText)
		}
	}
	if !terminalSeen {
		emitTerminal(llm.EndCancelled, "cancelled")
	}
	return nil
}

// Stop 中断指定会话的进行中轮次（幂等：无进行中轮次时为空操作）。
func (b *Bind) Stop(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cancel, ok := b.cancels[sessionID]; ok {
		cancel()
	}
}
