// 审批闸门的编排层实现：把内核的"执行前询问"桥接到 UI（问 → 等 → 答）。
// 设计要点见 ADR-0007；此处只承担"配对"职责，不做任何内容判断。
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"tiancode/internal/core/agent"
	"tiancode/internal/core/session"
)

// ApprovalEvent 是发给 UI 的审批请求（UI 渲染确认卡片）。
// SessionID 标明请求来自哪个会话：多会话并行（0.2.25）后，后台会话的审批卡
// 必须能归位到它自己的会话，而不是插进当前正在看的会话。
// SessionTitle（0.2.36 审计 R3）是确认卡上必须显示的会话名——用户在多会话
// 环境下要一眼知道"这条命令是哪个对话要跑的"。
type ApprovalEvent struct {
	ID           string `json:"id"`
	SessionID    string `json:"sessionID"`
	SessionTitle string `json:"sessionTitle,omitempty"`
	ToolName     string `json:"toolName"`
	Arguments    string `json:"arguments"` // 原始 JSON，原样展示，不解析（ADR-0007 第 2 条）
}

// approvalReplyTimeout 是审批等待的独立上限（0.2.36 审计 R3）：审批只跟随
// 本轮 ctx 会让"后台会话的确认卡"无限挂住整轮（用户没在看那个会话，队列也
// 不会动）。超时按**拒绝**处理——保护默认在，用户没确认就不执行（绝不放行）。
// 包级变量：测试注入短值验证超时路径。
var approvalReplyTimeout = 5 * time.Minute

// sessionTitleOf 取会话标题（审批卡展示用）；读不到回退会话 ID。
// 低频调用（每次审批一次只读扫描），不为它引入缓存（失效语义复杂）。
func (s *ChatService) sessionTitleOf(sessionID string) string {
	meta, err := session.ReadMeta(s.cfg.DataDir, sessionID)
	if err != nil || meta.Title == "" {
		return sessionID
	}
	return meta.Title
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
// approverFor 组装本轮审批器：UI 确认（uiApprover）+ 可选的工作区白名单旁路
// （shellAllowApprover，0.0.24）。root 由调用方传入：本函数在 s.mu 持有期间被调，
// 内部绝不能再碰 ledgerFor/sessionWorkspace（锁序纪律）。
func (s *ChatService) approverFor(sessionID, root string) agent.Approver {
	if len(s.approvalTools) == 0 {
		return nil
	}
	snapshot := make([]string, len(s.approvalTools))
	copy(snapshot, s.approvalTools)
	var inner agent.Approver = &uiApprover{svc: s, sessionID: sessionID, allowed: snapshot}
	if ws := loadWorkspaceSettings(root); len(ws.ShellAllow) > 0 {
		allow := make([]string, len(ws.ShellAllow))
		copy(allow, ws.ShellAllow)
		return &shellAllowApprover{inner: inner, allow: allow}
	}
	return inner
}

// shellAllowApprover 是审批白名单旁路：仅 shell、仅用户在工作区设置里显式
// 配置的前缀。命中即自动放行（Reason 留痕，模型与账本都看得见依据）；
// 不命中原样交给 UI 确认——白名单只会放行，绝不反向加严。
type shellAllowApprover struct {
	inner agent.Approver
	allow []string
}

func (a *shellAllowApprover) Review(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
	if req.ToolName == "shell" {
		var p struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(req.Arguments), &p) == nil {
			if pre, ok := shellAllowMatched(p.Command, a.allow); ok {
				return agent.Decision{Approved: true, Reason: "命中工作区审批白名单（前缀 " + pre + "）"}, nil
			}
		}
	}
	return a.inner.Review(ctx, req)
}

// shellAllowMatched 判定命令是否命中白名单（0.0.25 加固，纯函数可测）：
//   - 含组合/重定向/求值记号（& | ; < > 反引号、换行、$()、%）一律不命中——
//     "git status; rm -rf x" 的前缀匹配曾经放行过它（白名单发布当天审计抓到）；
//     % 是 cmd 的求值记号（%VAR% 展开 / 批处理参数），与 $() 同罪；
//     误伤的合法用法（如 git log --format=%h）转为走审批确认，是安全方向；
//   - 前缀命中后必须是词边界（rest 为空或以空白开始），"gitx" 不吃 "git" 的前缀；
//   - 空命令/空前缀不命中。
func shellAllowMatched(cmd string, allow []string) (string, bool) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", false
	}
	if strings.ContainsAny(cmd, "&|;<>`%\n\r") || strings.Contains(cmd, "$(") {
		return "", false
	}
	for _, pre := range allow {
		pre = strings.TrimSpace(pre)
		if pre == "" || !strings.HasPrefix(cmd, pre) {
			continue
		}
		rest := cmd[len(pre):]
		if rest == "" || rest[0] == ' ' || rest[0] == '\t' {
			return pre, true
		}
	}
	return "", false
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
		emit(ApprovalEvent{
			ID: id, SessionID: a.sessionID, SessionTitle: a.svc.sessionTitleOf(a.sessionID),
			ToolName: req.ToolName, Arguments: req.Arguments,
		})
	}

	// 独立超时（0.2.36 审计 R3）：到点按拒绝处理（理由回传给模型），绝不放行
	timer := time.NewTimer(approvalReplyTimeout)
	defer timer.Stop()
	select {
	case d := <-ch:
		return d, nil
	case <-timer.C:
		a.forget(id)
		return agent.Decision{
			Approved: false,
			Reason:   "审批超时（5 分钟内未收到答复），已按拒绝处理",
		}, nil
	case <-ctx.Done():
		a.forget(id)
		// 取消 → 由 agent 视为拒绝并说明原因（ADR-0007 第 4 条）
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
