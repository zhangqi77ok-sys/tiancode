package app

import "tiancode/internal/app"

// 后台任务快照绑定：把该会话 shell 工具内存里的后台任务表读给前端（右栏「任务」tab）。

// BgTasksSnapshot 返回 sessionID 这场对话的后台任务快照（id/命令/状态/日志）。
// 只读快照：会话没有 shell 工具集时返回空表而非错误（面板轮询不该被打断）；
// 快照内容在编排层 ChatService.BgTasksSnapshot，壳层只转发不吞错。
func (b *Bind) BgTasksSnapshot(sessionID string) []app.BgTaskView {
	return b.chat.BgTasksSnapshot(sessionID)
}
