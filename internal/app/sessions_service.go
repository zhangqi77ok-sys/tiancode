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

// SessionSummary 是会话列表项：ID + 用户标题（未重命名时 Title 为空）+ 归属工作区
// （账本首个 workspace 事件；旧会话为空，前端归入"未分组"）+ 置顶与最后活跃时间
// （侧栏分区与相对时间的数据源；LastActiveMs 为 0 表示全新会话未发生轮次）。
type SessionSummary struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Workspace    string `json:"workspace,omitempty"`
	Pinned       bool   `json:"pinned"`
	LastActiveMs int64  `json:"lastActiveMs"`
	// SkippedLines > 0 表示账本有未完成写入（回合进行中的常态）或坏行被跳过
	// （0.2.36 审计 R2：元数据只读、不截断、单条坏不联坐）。
	SkippedLines int `json:"skippedLines,omitempty"`
}

// maxTitleRunes 是标题长度上限：侧栏单行展示，过长既撑破布局也无法辨认。
const maxTitleRunes = 60

// PinSession 置顶/取消置顶会话（追加事件；重启/换机后仍生效）。
func (s *ChatService) PinSession(sessionID string, pinned bool) error {
	l, err := s.ledgerFor(sessionID)
	if err != nil {
		return err
	}
	if _, err := l.Append(session.EventSessionPinned, map[string]bool{"pinned": pinned}); err != nil {
		return fmt.Errorf("保存置顶状态失败：%w", err)
	}
	return nil
}

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
// 元数据走只读扫描（0.2.36 审计 R2）：此前每个会话 4 次 OpenLedger（写模式 +
// repair 截断）——回合进行中刷新列表会把正在追加的半行截掉（数据损坏）。
// 现在每会话一次 ReadMeta（不截断、坏行跳过计数）。
// 单条失败不联坐：某个账本不可读时该条降级为最小摘要（SkippedLines 标记），
// 其余会话照常返回——一个坏账本不能让整个侧栏消失。
func (s *ChatService) SessionSummaries() ([]SessionSummary, error) {
	ids, err := session.ListSessions(s.cfg.DataDir)
	if err != nil {
		return nil, err
	}
	out := make([]SessionSummary, 0, len(ids))
	for _, id := range ids {
		meta, metaErr := session.ReadMeta(s.cfg.DataDir, id)
		if metaErr != nil {
			// 该条不可读：以最小摘要呈现（时间仍可取），不牵连全体
			meta = session.Meta{Skipped: 1}
		}
		lastActive, err := session.LastActive(s.cfg.DataDir, id)
		if err != nil {
			lastActive = 0
		}
		out = append(out, SessionSummary{
			ID: id, Title: meta.Title, Workspace: meta.Workspace,
			Pinned: meta.Pinned, LastActiveMs: lastActive, SkippedLines: meta.Skipped,
		})
	}
	return out, nil
}
