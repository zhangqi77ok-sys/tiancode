// 用户命令行用例（0.3 最小能力）：执行"用户自己想跑"的一条命令。
//
// 纪律：执行器就是该会话的 shell 工具实例（同一超时、同一输出有界、同一取消语义），
// 不新开执行器；审批闸门含 shell 时与模型命令走**同一个**审批卡（uiApprover）。
// 结果不落账本：这是用户动作而非对话轮次（与"点链接开浏览器"的本地卡同理由）。
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tiancode/internal/core/agent"
)

// UserShellResult 是用户命令的执行回执（前端据此更新本地工具卡）。
type UserShellResult struct {
	Output   string `json:"output"`
	IsError  bool   `json:"isError"`
	TimedOut bool   `json:"timedOut"`
	Denied   bool   `json:"denied"` // 被审批拒绝（output 即拒绝说明）
}

// RunUserCommand 在这场对话的工作区执行用户输入的命令。
// 审批闸门（approvalTools 含 shell）开启时先弹审批卡等人确认——用户自己的命令
// 与模型的命令受同一道闸，绝无绕过路径。
func (s *ChatService) RunUserCommand(ctx context.Context, sessionID, command string) (UserShellResult, error) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return UserShellResult{}, errors.New("命令为空")
	}
	if !s.sessionLedgerOnDisk(sessionID) {
		return UserShellResult{}, fmt.Errorf("会话不存在或已删除：%s", sessionID)
	}
	root := s.sessionWorkspace(sessionID)
	if root == "" {
		return UserShellResult{}, errors.New("这场对话没有工作区，无法执行命令")
	}
	st, err := s.ensureSessionTools(sessionID, root)
	if err != nil {
		return UserShellResult{}, err
	}
	if st.shell == nil {
		return UserShellResult{}, errors.New("这场对话没有 shell 工具（纯对话模式）")
	}
	// 与模型命令同形参数：审批卡展示的 arguments 就是这份 JSON。
	args, err := json.Marshal(map[string]any{"action": "run", "command": cmd})
	if err != nil {
		return UserShellResult{}, fmt.Errorf("构造命令参数失败：%w", err)
	}
	// 审批：approverFor 在审批关闭时返回 nil（行为回到零干扰）；开启时 Review
	// 内部对不在清单内的工具直接放行——统一走 Review，不在这里重复判断清单。
	s.mu.Lock()
	approver := s.approverFor(sessionID, root)
	s.mu.Unlock()
	if approver != nil {
		decision, rerr := approver.Review(ctx, agent.ApprovalRequest{ToolName: "shell", Arguments: string(args)})
		if rerr != nil {
			return UserShellResult{}, fmt.Errorf("审批通道异常，已拒绝执行：%w", rerr)
		}
		if !decision.Approved {
			reason := decision.Reason
			if reason == "" {
				reason = "用户未批准"
			}
			return UserShellResult{Output: "用户拒绝执行：" + reason, IsError: true, Denied: true}, nil
		}
	}
	res, err := st.shell.Execute(ctx, args)
	if err != nil {
		return UserShellResult{}, fmt.Errorf("命令执行失败：%w", err)
	}
	return UserShellResult{Output: res.Content, IsError: res.IsError, TimedOut: res.TimedOut}, nil
}
