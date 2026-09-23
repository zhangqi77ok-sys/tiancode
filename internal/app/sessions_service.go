// 会话管理用例：重命名与列表摘要。
// 标题以"账本事件"为事实源（core/session.EventSessionRenamed），因此重启/崩溃恢复后仍在。
package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

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

// roleLabel 把消息角色映射为中文小节标题（未知角色原样保留，不丢信息）。
func roleLabel(role string) string {
	switch role {
	case "user":
		return "用户"
	case "assistant":
		return "助手"
	case "tool":
		return "工具"
	default:
		return role
	}
}

// ExportSessionMarkdown 把会话导出为 Markdown 文本（交由前端复制/保存）。
// 复用界面同源的账本投影（Replay）：导出内容 = 用户所见，不引入第二事实源。
func (s *ChatService) ExportSessionMarkdown(sessionID string) (string, error) {
	title, err := session.Title(s.cfg.DataDir, sessionID)
	if err != nil {
		return "", err
	}
	if title == "" {
		title = sessionID // 未重命名：标题回退会话 ID（导出仍可用）
	}
	msgs, err := s.Replay(sessionID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "> 会话 %s · 导出于 %s\n", sessionID, time.Now().Format("2006-01-02 15:04"))
	for _, m := range msgs {
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", roleLabel(m.Role), m.Content)
	}
	return b.String(), nil
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
		title, err := session.Title(s.cfg.DataDir, id)
		if err != nil {
			return nil, fmt.Errorf("读取会话 %s 标题失败：%w", id, err)
		}
		out = append(out, SessionSummary{ID: id, Title: title})
	}
	return out, nil
}
