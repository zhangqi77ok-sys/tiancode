package agent

import (
	"context"
	"fmt"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/tools"
)

// ApprovalRequest 是一次工具执行前的确认请求（只带工具名与原始参数，不做语义分析）。
type ApprovalRequest struct {
	ToolName  string // 工具名（审批清单按它匹配）
	Arguments string // 原始 JSON 参数（原样展示给用户判断，不解析）
}

// Decision 是用户对确认请求的答复。
type Decision struct {
	Approved bool
	Reason   string // 拒绝原因（回填给模型，便于改道）
}

// Approver 是审批端口（Strategy，ADR-0007）。
// 为什么是端口而不是拦截链：决策权在用户，代码只负责"问"与"等待"，
// 不做任何内容分析——旧 astguard 用 AST 猜意图导致误杀，此设计从结构上排除。
type Approver interface {
	Review(ctx context.Context, req ApprovalRequest) (Decision, error)
}

// SetApprover 注入审批器；nil 表示不启用（默认关闭，行为与历史版本一致）。
func (l *Loop) SetApprover(a Approver) { l.approver = a }

// execToolWithApproval 是工具执行的唯一入口：审批关时等价于 execTool。
//
// 三条纪律（ADR-0007）：
//   - 拒绝 → 模型可见的失败结果（可据原因改道），绝不静默拦截；
//   - 审批通道故障 → 也算拒绝（故障时放行等于让"本该确认的命令"静默执行）；
//   - 用户未给原因 → 补默认原因，避免模型收到空解释。
func (l *Loop) execToolWithApproval(ctx context.Context, call llm.ToolCall) tools.ToolResult {
	if l.approver == nil {
		return l.execTool(ctx, call)
	}
	decision, err := l.approver.Review(ctx, ApprovalRequest{
		ToolName:  call.Name,
		Arguments: call.Arguments,
	})
	if err != nil {
		return tools.ToolResult{
			Content: fmt.Sprintf("审批通道异常，已拒绝执行 %s：%v", call.Name, err),
			IsError: true,
		}
	}
	if !decision.Approved {
		reason := decision.Reason
		if reason == "" {
			reason = "用户未批准"
		}
		return tools.ToolResult{
			Content: fmt.Sprintf("用户拒绝执行 %s：%s（如需继续请换一种方式或征得同意后重试）", call.Name, reason),
			IsError: true,
		}
	}
	return l.execTool(ctx, call)
}
