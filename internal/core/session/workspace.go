package session

import (
	"encoding/json"
	"fmt"
)

// EventWorkspace 记录"本轮发送时的工作区"快照（ChatService.Send 每轮追加，一行 ~60B）。
// 会话归属工作区 = 账本首个 workspace 事件：侧栏按空间分组的唯一依据；
// 0.2.6 及之前的账本没有该事件，Workspace 返回空串（前端归入"未分组"，向后兼容）。
const EventWorkspace EventKind = "workspace"

// Workspace 重放账本取首个工作区快照；从未记录则返回空串（旧账本兼容）。
// 命名遵循 Title 的先例：调用方已是 session.Workspace，再加前缀会重复
// （revive: exported 的 stutter 规则）。
func Workspace(dir, sessionID string) (string, error) {
	l, err := OpenLedger(dir, sessionID)
	if err != nil {
		return "", err
	}
	ws := ""
	replayErr := l.Replay(func(e Event) error {
		if e.Kind() != EventWorkspace {
			return nil
		}
		var payload struct {
			Path string `json:"path"`
		}
		// 坏事件必须上抛（R2）：静默跳过会表现为"分组莫名丢失"
		if err := json.Unmarshal(e.Data(), &payload); err != nil {
			return fmt.Errorf("工作区事件解析失败（会话 %s）：%w", sessionID, err)
		}
		// 只认首个：会话归属 = 首次发送时的工作区，中途切换不改变归属
		if ws == "" && payload.Path != "" {
			ws = payload.Path
		}
		return nil
	})
	if err := l.Close(); err != nil {
		return "", err
	}
	if replayErr != nil {
		return "", replayErr
	}
	return ws, nil
}
