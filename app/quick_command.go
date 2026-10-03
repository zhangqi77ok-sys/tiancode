// 工作区快捷命令的绑定（0.0.26）：build/test/run 三槽一键跑（校验与执行都在编排层）。
package app

import "tiancode/internal/app"

// QuickCommandSlots 是三个槽位（值 = 用户配的命令；空串 = 未配置）。
type QuickCommandSlots struct {
	Build string `json:"build"`
	Test  string `json:"test"`
	Run   string `json:"run"`
}

// QuickCommands 返回这场对话工作区的三个快捷命令（未配置的槽为空串）。
func (b *Bind) QuickCommands(sessionID string) QuickCommandSlots {
	q := b.chat.QuickCommandsOf(sessionID)
	return QuickCommandSlots{Build: q.Build, Test: q.Test, Run: q.Run}
}

// RunQuickCommand 执行一个槽位的命令。执行走 RunUserCommand 同一条链
// （同一执行器/超时/审批闸门）——快捷命令只是"可点的命令"，不新增执行能力。
func (b *Bind) RunQuickCommand(sessionID, slot, command string) (app.UserShellResult, error) {
	return b.chat.RunQuickCommand(b.appCtx(), sessionID, slot, command)
}
