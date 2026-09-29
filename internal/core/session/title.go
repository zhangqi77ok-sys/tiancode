package session

// EventSessionRenamed 是会话重命名事件。
// 为什么用事件而不是 sidecar 元数据文件：标题与消息同源（账本），
// 重放到任意时刻都得到一致视图，也不会有"两个文件不同步"的老问题。
const EventSessionRenamed EventKind = "session_renamed"

// Title 取最新标题；从未重命名过则返回空串（UI 回退显示会话 ID）。
// 走只读元数据扫描（0.2.36 审计 R2）：绝不 Truncate/repair——回合进行中
// 刷新列表不能截掉正在追加的半行；损坏行跳过（ReadMeta.Skipped 可观测）。
// 命名刻意不带 Session 前缀：调用方已是 session.Title，再加前缀会重复
// （revive: exported 的 stutter 规则会拒绝 session.SessionTitle）。
func Title(dir, sessionID string) (string, error) {
	m, err := ReadMeta(dir, sessionID)
	if err != nil {
		return "", err
	}
	return m.Title, nil
}
