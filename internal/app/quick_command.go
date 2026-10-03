// 工作区快捷命令（0.0.26 编排层）：build/test/run 三槽的执行与校验。
//
// 执行路径与用户自己在命令行里敲的**完全同一条**（RunUserCommand → 同一 shell
// 工具、同一超时、同一审批闸门、同一工作区校验）——快捷命令不新增任何执行能力，
// 只是把用户自己写的命令放到一个可点的位置。
package app

import (
	"context"
	"fmt"
)

// RunQuickCommand 执行一个槽位的命令。slot 只认 build/test/run；命令为空
// （槽位未配置）显式拒绝，绝不猜命令。
func (s *ChatService) RunQuickCommand(ctx context.Context, sessionID, slot, command string) (UserShellResult, error) {
	switch slot {
	case "build", "test", "run":
	default:
		return UserShellResult{}, fmt.Errorf("未知的快捷命令槽位：%q（只认 build/test/run）", slot)
	}
	if command == "" {
		return UserShellResult{}, fmt.Errorf("该槽位尚未配置命令：先在工作区设置里填")
	}
	return s.RunUserCommand(ctx, sessionID, command)
}

// QuickCommandsOf 读这场对话工作区的三个槽（未配置为空串）。
func (s *ChatService) QuickCommandsOf(sessionID string) QuickCommands {
	return loadWorkspaceSettings(s.sessionWorkspace(sessionID)).QuickCommands
}
