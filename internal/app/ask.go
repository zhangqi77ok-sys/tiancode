// 问答交互的编排层实现：把内核的 ask_user 桥接到 UI（问 → 等 → 答）。
// 与审批闸门（approval.go，ADR-0007）同构：只承担"配对"职责，不做内容判断。
package app

import (
	"context"
	"fmt"
	"sync/atomic"

	"tiancode/internal/core/agent"
)

// AskEvent 是发给 UI 的问答请求（UI 渲染选项卡，答复经 ResolveAsk 回流）。
// SessionID 标明请求来自哪个会话（多会话并行后，后台会话的问答卡必须归位到
// 它自己的会话，见 approval.go 同款注释）。
type AskEvent struct {
	ID        string   `json:"id"`
	SessionID string   `json:"sessionID"`
	Question  string   `json:"question"`
	Options   []string `json:"options"` // 可空 = 自由回答
}

// SetAskHandler 注入问答事件回调（壳层负责推送到前端）；nil 表示只等不通知（测试用）。
func (s *ChatService) SetAskHandler(fn func(AskEvent)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.askEmit = fn
}

// ResolveAsk 提交用户对某次问答的答复（答案原样回流给模型）。
// 未知或已处理的 ID 一律报错（与 ResolveApproval 同纪律：可见，不静默）。
func (s *ChatService) ResolveAsk(id string, answer string) error {
	s.mu.Lock()
	ch, ok := s.pendingAsks[id]
	if ok {
		delete(s.pendingAsks, id)
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("问答请求不存在或已处理：%s", id)
	}
	select {
	case ch <- answer:
		return nil
	default:
		return fmt.Errorf("问答请求已被处理：%s", id)
	}
}

// uiAsker 是 agent.Asker 的编排层实现：发事件 → 等答复 → 返回答案。
// sessionID 是发起这轮对话的会话（随轮注入，见 ChatService.newAgentWith）。
type uiAsker struct {
	svc       *ChatService
	sessionID string
}

// askSeq 生成请求 ID（进程内唯一即可，仅用于 UI 与答复配对）。
var askSeq atomic.Int64

func (a *uiAsker) Ask(ctx context.Context, req agent.AskRequest) (string, error) {
	id := fmt.Sprintf("ask-%d", askSeq.Add(1))
	ch := make(chan string, 1)

	a.svc.mu.Lock()
	if a.svc.pendingAsks == nil {
		a.svc.pendingAsks = make(map[string]chan string)
	}
	a.svc.pendingAsks[id] = ch
	emit := a.svc.askEmit
	a.svc.mu.Unlock()

	if emit != nil {
		emit(AskEvent{ID: id, SessionID: a.sessionID, Question: req.Question, Options: req.Options})
	}

	select {
	case answer := <-ch:
		return answer, nil
	case <-ctx.Done():
		a.forget(id)
		// 取消/超时 → agent 视为失败并说明原因（与审批第 4 条纪律一致）
		return "", ctx.Err()
	}
}

// forget 清理未决请求（取消路径），避免挂起项泄漏。
func (a *uiAsker) forget(id string) {
	a.svc.mu.Lock()
	defer a.svc.mu.Unlock()
	delete(a.svc.pendingAsks, id)
}
