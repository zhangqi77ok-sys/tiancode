package session

import (
	"encoding/json"
	"fmt"
)

// EventSessionRenamed 是会话重命名事件。
// 为什么用事件而不是 sidecar 元数据文件：标题与消息同源（账本），
// 重放到任意时刻都得到一致视图，也不会有"两个文件不同步"的老问题。
const EventSessionRenamed EventKind = "session_renamed"

// Title 重放账本取最新标题；从未重命名过则返回空串（UI 回退显示会话 ID）。
// 命名刻意不带 Session 前缀：调用方已是 session.Title，再加前缀会重复
// （revive: exported 的 stutter 规则会拒绝 session.SessionTitle）。
func Title(dir, sessionID string) (string, error) {
	l, err := OpenLedger(dir, sessionID)
	if err != nil {
		return "", err
	}
	title := ""
	replayErr := l.Replay(func(e Event) error {
		if e.Kind() != EventSessionRenamed {
			return nil
		}
		var payload struct {
			Title string `json:"title"`
		}
		// 坏事件必须上抛（R2）：静默跳过会表现为"标题莫名丢失"
		if err := json.Unmarshal(e.Data(), &payload); err != nil {
			return fmt.Errorf("重命名事件解析失败（会话 %s）：%w", sessionID, err)
		}
		title = payload.Title
		return nil
	})
	if err := l.Close(); err != nil {
		return "", err
	}
	if replayErr != nil {
		return "", replayErr
	}
	return title, nil
}
