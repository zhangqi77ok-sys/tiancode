// 后台任务快照用例：把会话 shell 工具内存里的任务表投影给前端（右栏「任务」tab）。
// 做什么：按会话取工具集，读 shelltool 的任务快照并转成界面视图。
// 被谁依赖：壳层（Wails 绑定 app/）。
// 依赖谁：platform/shelltool（数据源）；任务真相只在工具实例内存里，不落账本。
package app

import "tiancode/internal/platform/shelltool"

// BgTaskView 是一场对话的后台任务快照单项（id/命令/状态/日志）。
// StartedAt/ModTime 同款纪律：Unix 毫秒，排版交给前端。
type BgTaskView struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	PID       int    `json:"pid"`
	Running   bool   `json:"running"`
	ExitCode  int    `json:"exitCode"`
	StartedAt int64  `json:"startedAt"`
	Log       string `json:"log"` // 头尾保留的有界输出（已解码；与 bg_status 看到的同一份）
}

// BgTasksSnapshot 返回该会话 shell 工具的后台任务快照（按任务号升序）。
// 会话没有工具集（纯对话 / 还没发过消息）= 空表而非错误：面板轮询不该被
// "没有任务"打断。锁纪律：sessTools 读写都在 sessMu 内（第二轮体检 R2 起
// 与 ensureSessionTools 同款专用锁，不再用 s.mu），任务表自身的并发安全由
// shelltool 负责。
func (s *ChatService) BgTasksSnapshot(sessionID string) []BgTaskView {
	s.sessMu.Lock()
	st, ok := s.sessTools[sessionID]
	s.sessMu.Unlock()
	if !ok || st == nil || st.shell == nil {
		return []BgTaskView{}
	}
	src, ok := st.shell.(interface{ BgTasksSnapshot() []shelltool.BgTaskInfo })
	if !ok {
		return []BgTaskView{} // 非 shelltool 实例（理论不可达）：按无任务处理，不虚构
	}
	infos := src.BgTasksSnapshot()
	out := make([]BgTaskView, 0, len(infos))
	for _, it := range infos {
		out = append(out, BgTaskView{
			ID:        it.ID,
			Command:   it.Command,
			PID:       it.PID,
			Running:   it.Running,
			ExitCode:  it.ExitCode,
			StartedAt: it.StartedAt.UnixMilli(),
			Log:       it.Log,
		})
	}
	return out
}
