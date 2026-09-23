// 会话管理用例：重命名与列表摘要。
// 标题以"账本事件"为事实源（core/session.EventSessionRenamed），因此重启/崩溃恢复后仍在。
package app

import (
	"errors"
	"fmt"
	"strings"

	"tiancode/internal/core/session"
)

// SessionSummary 是会话列表项：ID + 用户标题（未重命名时 Title 为空）。
type SessionSummary struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// maxTitleRunes 是标题长度上限：侧栏单行展示，过长既撑破布局也无法辨认。
const maxTitleRunes = 60

// RenameSession 追加重命名事件。
func (s *ChatService) RenameSession(sessionID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("会话标题不能为空")
	}
	if len([]rune(title)) > maxTitleRunes {
		return fmt.Errorf("会话标题过长（最多 %d 字）", maxTitleRunes)
	}
	l, err := s.ledgerFor(sessionID) // 内部含会话 ID 合法性校验
	if err != nil {
		return err
	}
	if _, err := l.Append(session.EventSessionRenamed, map[string]string{"title": title}); err != nil {
		return fmt.Errorf("保存会话标题失败：%w", err)
	}
	return nil
}

// SessionSummaries 返回全部会话摘要（按 ID 排序，与 ListSessions 一致）。
// 单个账本损坏时上抛错误：静默跳过会让用户以为"会话凭空消失"（R2 禁吞错）。
func (s *ChatService) SessionSummaries() ([]SessionSummary, error) {
	ids, err := session.ListSessions(s.cfg.DataDir)
	if err != nil {
		return nil, err
	}
	out := make([]SessionSummary, 0, len(ids))
	for _, id := range ids {
		title, err := session.SessionTitle(s.cfg.DataDir, id)
		if err != nil {
			return nil, fmt.Errorf("读取会话 %s 标题失败：%w", id, err)
		}
		out = append(out, SessionSummary{ID: id, Title: title})
	}
	return out, nil
}
