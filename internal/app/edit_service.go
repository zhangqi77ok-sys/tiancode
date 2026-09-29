package app

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"tiancode/internal/platform/fstool"
)

// EditEvent 是一次待确认文件变更（0.0.10）：推给界面渲染确认卡
// （路径 + 短 diff + 新建/修改），用户「应用/跳过」后经 ResolveEdit 回流。
type EditEvent struct {
	ID           string `json:"id"`
	SessionID    string `json:"sessionID"`
	SessionTitle string `json:"sessionTitle,omitempty"`
	CallID       string `json:"callId,omitempty"` // 与工具卡配对（终态原地更新）
	Path         string `json:"path"`
	Diff         string `json:"diff"`
	IsNew        bool   `json:"isNew"`
}

// editSeq 生成确认请求 ID（进程内唯一，用于 UI 与答复配对）。
var editSeq atomic.Int64

// chatEditGate 把 fstool 的确认请求桥接到 UI：发事件 → 等用户答复 → 执行 Apply。
// 纪律（0.0.10）：未确认不落盘；取消按跳过；**没有超时自动应用**。
type chatEditGate struct {
	svc       *ChatService
	sessionID string
}

func (g *chatEditGate) ConfirmEdit(ctx context.Context, p fstool.EditProposal) (bool, *fstool.UndoSnapshot, error) {
	s := g.svc
	id := fmt.Sprintf("ed-%d", editSeq.Add(1))
	ch := make(chan bool, 1)

	s.mu.Lock()
	if s.pendingEdits == nil {
		s.pendingEdits = make(map[string]chan bool)
	}
	s.pendingEdits[id] = ch
	emit := s.editEmit
	title := s.sessionTitleOf(g.sessionID)
	s.mu.Unlock()

	if emit != nil {
		emit(EditEvent{
			ID: id, SessionID: g.sessionID, SessionTitle: title,
			CallID: p.CallID, Path: p.Path, Diff: p.Diff, IsNew: p.IsNew,
		})
	}

	select {
	case applied := <-ch:
		if !applied {
			// 用户跳过：不落盘。结果文本由工具返回给模型（写明未修改）。
			deletePendingEdit(s, id)
			return false, nil, nil
		}
	case <-ctx.Done():
		// 会话取消 = 按跳过（不写入），终态由 agent 上抛 EndCancelled
		deletePendingEdit(s, id)
		return false, nil, nil
	}

	u, err := p.Apply()
	if err != nil {
		// 应用失败（外部改动等）：原文件不动，错误回传给模型与界面
		return true, nil, err
	}
	return true, u, nil
}

func deletePendingEdit(s *ChatService, id string) {
	s.mu.Lock()
	delete(s.pendingEdits, id)
	s.mu.Unlock()
}

// SetEditHandler 注入确认事件回调（壳层负责推送到前端）；nil 表示只等不通知（测试用）。
func (s *ChatService) SetEditHandler(fn func(EditEvent)) {
	s.mu.Lock()
	s.editEmit = fn
	s.mu.Unlock()
}

// ResolveEdit 提交用户对某次文件变更的答复（应用/跳过）。
// 未知或已处理的 ID 显式报错（UI 重复提交可见，不静默）。
func (s *ChatService) ResolveEdit(sessionID, editID string, apply bool) error {
	s.mu.Lock()
	ch, ok := s.pendingEdits[editID]
	if ok {
		delete(s.pendingEdits, editID)
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("确认请求不存在或已处理：%s", editID)
	}
	select {
	case ch <- apply:
		return nil
	case <-time.After(3 * time.Second):
		return fmt.Errorf("确认答复送达失败（请求已结束）")
	}
}

// ProposeFileWrite 把「应用到文件」的代码块内容变成一次待确认变更（0.0.10）：
// 与模型 write 同一条确认链路——先给人看 diff，确认才落盘，取消不写。
// 目标文件由用户显式选择，绕过模型的整读门卫（用户确认是更高授权）；
// 但保留外部改动检测：应用时文件与提案时不一致就拒绝。
func (s *ChatService) ProposeFileWrite(sessionID, path, content string) error {
	root := s.sessionWorkspace(sessionID)
	if root == "" {
		return fmt.Errorf("这场对话没有工作区，无法写入文件")
	}
	st, err := s.ensureSessionTools(sessionID, root)
	if err != nil {
		return err
	}
	return st.fs.ProposeWrite(context.Background(), path, content)
}

// sessionWorkspace 返回这场对话自己的工作区根（账本首个 workspace 事件；
// 草稿/纯对话返回空）。@ 引用与代码块应用都以它为准——不用顶栏里
// "下一场新对话"的根（0.0.10 用户要求）。
func (s *ChatService) sessionWorkspace(sessionID string) string {
	ledger, err := s.ledgerFor(sessionID)
	if err != nil {
		return ""
	}
	root, owned := firstWorkspaceOfLedger(ledger)
	if !owned {
		return ""
	}
	return normalizeWorkspace(root)
}

// SessionWorkspace 是 sessionWorkspace 的导出形式（壳层 @ 引用用）。
func (s *ChatService) SessionWorkspace(sessionID string) string { return s.sessionWorkspace(sessionID) }
