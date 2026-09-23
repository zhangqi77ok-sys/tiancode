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
type Bind struct {
	chat    *app.ChatService
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// New 装配壳层。
func New(chat *app.ChatService) *Bind {
	return &Bind{chat: chat, cancels: make(map[string]context.CancelFunc)}
}

// ListSessions 返回全部会话 ID。
func (b *Bind) ListSessions() ([]string, error) {
	return b.chat.ListSessions()
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
func (b *Bind) Send(ctx context.Context, sessionID, text string) error {
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
