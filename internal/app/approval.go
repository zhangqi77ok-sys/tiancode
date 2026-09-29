// 审批闸门的编排层实现：把内核的"执行前询问"桥接到 UI（问 → 等 → 答）。
// 设计要点见 ADR-0007；此处只承担"配对"职责，不做任何内容判断。
package app

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"tiancode/internal/core/agent"
)

// ApprovalEvent 是发给 UI 的审批请求（UI 渲染确认卡片）。
// SessionID 标明请求来自哪个会话：多会话并行（0.2.25）后，后台会话的审批卡
// 必须能归位到它自己的会话，而不是插进当前正在看的会话。
type ApprovalEvent struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	ToolName  string `json:"toolName"`
	Arguments string `json:"arguments"` // 原始 JSON，原样展示，不解析（ADR-0007 第 2 条）
}

// SetApprovalHandler 注入审批事件回调（壳层负责推送到前端）；nil 表示只等不通知（测试用）。
func (s *ChatService) SetApprovalHandler(fn func(ApprovalEvent)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvalEmit = fn
}

// ApprovalPolicy 返回当前需要审批的工具清单（空 = 审批功能关闭）。
func (s *ChatService) ApprovalPolicy() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.approvalTools))
	copy(out, s.approvalTools)
	return out
}

// SetApprovalPolicy 设置需要审批的工具清单；传空即关闭审批（默认关，ADR-0007 第 1 条）。
// **持久化到用户级配置**：策略变更立刻生效且重启后仍有效（否则用户每次启动都要重开
// 开关，属于基础体验缺失）。生效语义：agent 改为每轮独立构建（多会话并行），
// 审批器随轮注入，因此变更从**下一轮**开始生效；进行中的轮次维持开跑时的策略。
func (s *ChatService) SetApprovalPolicy(toolNames []string) error {
	cleaned := make([]string, 0, len(toolNames))
	for _, n := range toolNames {
		n = strings.TrimSpace(n)
		if n != "" {
			cleaned = append(cleaned, n)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// 先落盘再改内存：落盘失败时内存保持不变，避免"界面显示已开、重启却没了"
	if err := s.pool.SetApprovalTools(cleaned); err != nil {
		return fmt.Errorf("保存审批策略失败：%w", err)
	}
	s.approvalTools = cleaned
	return nil
}

// approverFor 返回当前策略对应的审批器（调用方持锁；nil = 审批关闭，行为回到"零干扰"）。
// 为什么不再是"装到 agent 上"：agent 每轮独立构建（多会话并行），审批器随轮注入；
// sessionID 让审批事件能归属到发起它的会话。
// 审批工具清单**拷贝快照**（0.2.35 审计#7）：此前 Review 每次读活列表，回合中途
// 把某工具移出清单会让同回合的下一次调用直接放行——"进行中的轮次维持开跑时的
// 策略"的注释从未成立。快照后新策略仍从下一轮开始生效（与注释一致）。
func (s *ChatService) approverFor(sessionID string) agent.Approver {
	if len(s.approvalTools) == 0 {
		return nil
	}
	snapshot := make([]string, len(s.approvalTools))
	copy(snapshot, s.approvalTools)
	return &uiApprover{svc: s, sessionID: sessionID, allowed: snapshot}
}

// ResolveApproval 提交用户对某次审批请求的答复。
// 未知或已处理的 ID 一律报错（含已处理）：UI 重复提交时用户会看到明确提示，
// 而不是"点了没反应"或"悄悄放行"。
func (s *ChatService) ResolveApproval(id string, approved bool, reason string) error {
	s.mu.Lock()
	ch, ok := s.pendingApprovals[id]
	if ok {
		delete(s.pendingApprovals, id)
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("审批请求不存在或已处理：%s", id)
	}
	select {
	case ch <- agent.Decision{Approved: approved, Reason: reason}:
		return nil
	default:
		// 通道缓冲为 1，正常不会走到这里；走到说明同一 ID 被并发解决
		return fmt.Errorf("审批请求已被处理：%s", id)
	}
}

// uiApprover 是 agent.Approver 的编排层实现：发事件 → 等答复 → 返回决策。
// sessionID 是发起这轮对话的会话（随轮注入，见 ChatService.newAgentWith）。
// allowed 是本回合的审批清单快照（approverFor 拷贝）：策略变更只影响下一轮。
type uiApprover struct {
	svc       *ChatService
	sessionID string
	allowed   []string
}

// approvalSeq 生成请求 ID（进程内唯一即可，仅用于 UI 与答复配对）。
var approvalSeq atomic.Int64

func (a *uiApprover) Review(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
	// 不在本回合快照内的工具直接放行：审批只针对用户指定的工具，绝不做泛化拦截。
	// 用快照而非活列表（0.2.35 审计#7）：回合中途改策略不改变本回合行为。
	found := false
	for _, n := range a.allowed {
		if n == req.ToolName {
			found = true
			break
		}
	}
	if !found {
		return agent.Decision{Approved: true}, nil
	}

	id := fmt.Sprintf("ap-%d", approvalSeq.Add(1))
	ch := make(chan agent.Decision, 1)

	a.svc.mu.Lock()
	if a.svc.pendingApprovals == nil {
		a.svc.pendingApprovals = make(map[string]chan agent.Decision)
	}
	a.svc.pendingApprovals[id] = ch
	emit := a.svc.approvalEmit
	a.svc.mu.Unlock()

	if emit != nil {
		emit(ApprovalEvent{ID: id, SessionID: a.sessionID, ToolName: req.ToolName, Arguments: req.Arguments})
	}

	select {
	case d := <-ch:
		return d, nil
	case <-ctx.Done():
		a.forget(id)
		// 取消/超时 → 由 agent 视为拒绝并说明原因（ADR-0007 第 4 条）
		return agent.Decision{}, ctx.Err()
	}
}

// forget 清理未决请求（取消路径），避免挂起项泄漏。
func (a *uiApprover) forget(id string) {
	a.svc.mu.Lock()
	defer a.svc.mu.Unlock()
	delete(a.svc.pendingApprovals, id)
}

// toolNeedsApproval 判断某工具是否在审批清单内（精确匹配，不做模糊/语义判断）。
func (s *ChatService) toolNeedsApproval(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.approvalTools {
		if n == name {
			return true
		}
	}
	return false
}
