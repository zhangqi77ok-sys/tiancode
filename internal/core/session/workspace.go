package session

// EventWorkspace 记录"本轮发送时的工作区"快照（ChatService.Send 每轮追加，一行 ~60B）。
// 会话归属工作区 = 账本首个 workspace 事件：侧栏按空间分组的唯一依据；
// 0.2.6 及之前的账本没有该事件，Workspace 返回空串（前端归入"未分组"，向后兼容）。
const EventWorkspace EventKind = "workspace"

// Workspace 取首个工作区快照（会话归属）；从未记录则返回空串（旧账本兼容）。
// 走只读元数据扫描（0.2.36 审计 R2，绝不截断）。首个事件即归属——含显式空
// （纯对话会话必须保持空，不能因为"没有记录"被后续事件套上别的目录）。
// 命名遵循 Title 的先例：调用方已是 session.Workspace，再加前缀会重复
// （revive: exported 的 stutter 规则）。
func Workspace(dir, sessionID string) (string, error) {
	m, err := ReadMeta(dir, sessionID)
	if err != nil {
		return "", err
	}
	return m.Workspace, nil
}
