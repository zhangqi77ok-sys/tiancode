package session

import (
	"fmt"
	"os"
	"path/filepath"
)

// EventSessionPinned 记录置顶/取消置顶（pinned: true/false，取最后一条为当前状态）。
// 为什么用事件：置顶与标题同源（账本），重启/换机后仍生效，不引入第二事实源。
const EventSessionPinned EventKind = "session_pinned"

// Pinned 取最后一次置顶事件的状态；从未置顶返回 false。
// 走只读元数据扫描（0.2.36 审计 R2，绝不截断）。命名遵循 Title/Workspace
// 的先例（stutter 规则）。
func Pinned(dir, sessionID string) (bool, error) {
	m, err := ReadMeta(dir, sessionID)
	if err != nil {
		return false, err
	}
	return m.Pinned, nil
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
