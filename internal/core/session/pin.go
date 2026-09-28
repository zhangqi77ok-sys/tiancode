package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// EventSessionPinned 记录置顶/取消置顶（pinned: true/false，取最后一条为当前状态）。
// 为什么用事件：置顶与标题同源（账本），重启/换机后仍生效，不引入第二事实源。
const EventSessionPinned EventKind = "session_pinned"

// Pinned 重放账本取最后一次置顶事件；从未置顶返回 false。
// 命名遵循 Title/Workspace 的先例（stutter 规则）。
func Pinned(dir, sessionID string) (bool, error) {
	l, err := OpenLedger(dir, sessionID)
	if err != nil {
		return false, err
	}
	pinned := false
	replayErr := l.Replay(func(e Event) error {
		if e.Kind() != EventSessionPinned {
			return nil
		}
		var payload struct {
			Pinned bool `json:"pinned"`
		}
		// 坏事件必须上抛（R2）：静默跳过会表现为"置顶莫名丢失"
		if err := json.Unmarshal(e.Data(), &payload); err != nil {
			return fmt.Errorf("置顶事件解析失败（会话 %s）：%w", sessionID, err)
		}
		pinned = payload.Pinned
		return nil
	})
	if err := l.Close(); err != nil {
		return false, err
	}
	if replayErr != nil {
		return false, replayErr
	}
	return pinned, nil
}

// LastActive 返回账本文件的最后修改时间（UnixMilli）：任何追加（消息/工具/终态）
// 都会推进文件修改时间，故它就是"最后活跃时刻"的忠实事实源，无需新事件。
// 文件不存在（全新会话尚未发生任何轮次）返回 0，前端不显示时间。
func LastActive(dir, sessionID string) (int64, error) {
	if err := validateSessionID(sessionID); err != nil {
		return 0, err
	}
	info, err := os.Stat(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("读取会话 %s 活跃时间失败：%w", sessionID, err)
	}
	return info.ModTime().UnixMilli(), nil
}
