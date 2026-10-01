package app

import (
	"fmt"

	"tiancode/internal/core/session"
)

// ProposeWriteResult 是「应用到文件」的写入回执（UI 渲染一张写入卡：路径 + diff + 新建/覆盖）。
// 为什么不复用工具事件：这是用户主动动作（非模型轮次），不参与工具卡配对，
// 同步返回给调用方是最小且诚实的表达（与 RestoreToolWrite 返回说明同构）。
// 第 6 批起写入结果同时落账本（EventUserEdit），Replay 后这张卡仍在。
type ProposeWriteResult struct {
	Path  string `json:"path"`
	IsNew bool   `json:"isNew"`
	Diff  string `json:"diff"`
	Bytes int    `json:"bytes"`
}

// ProposeFileWrite 把「应用到文件」的代码块内容直接写入工作区（改了就是改了，
// 0.0.10 的确认卡链路已移除）：目标文件由用户显式选择，绕过模型的整读门卫
// （用户确认是更高授权）；写入结果（diff/新建标记）同步返回，界面据此出回执卡。
// 目标文件在读取快照后被其他程序改过时拒绝写入（原文件不动），错误原样上抛。
//
// 第 6 批：写入成功落账本（EventUserEdit，含写入前内容与超限说明）——
// 重启/切回会话后卡片仍在（Replay 投影），派生历史时模型收到一句"已应用过"。
func (s *ChatService) ProposeFileWrite(sessionID, path, content string) (ProposeWriteResult, error) {
	// 存活校验必须在 sessionWorkspace（会经 ledgerFor 重开账本、给已删除会话
	// 重建空文件）之前：权威判据是账本文件还在盘上（第二轮体检 R3——绝不给
	// 已删除会话重建无主工具集）。
	if !s.sessionLedgerOnDisk(sessionID) {
		return ProposeWriteResult{}, fmt.Errorf("会话不存在或已删除：%s", sessionID)
	}
	root := s.sessionWorkspace(sessionID)
	if root == "" {
		return ProposeWriteResult{}, fmt.Errorf("这场对话没有工作区，无法写入文件")
	}
	st, err := s.ensureSessionTools(sessionID, root)
	if err != nil {
		return ProposeWriteResult{}, err
	}
	res, err := st.fs.ProposeWrite(path, content)
	if err != nil {
		return ProposeWriteResult{}, err
	}
	out := ProposeWriteResult{Path: res.Path, IsNew: res.IsNew, Diff: res.Diff, Bytes: res.Bytes}
	ledger, lerr := s.ledgerFor(sessionID)
	if lerr != nil {
		return out, fmt.Errorf("写入成功，但账本打开失败（这张卡重启后会丢失）：%w", lerr)
	}
	payload := map[string]any{
		"path": res.Path, "is_new": res.IsNew, "bytes": res.Bytes, "diff": res.Diff,
	}
	if res.Note != "" {
		payload["note"] = res.Note
	}
	if res.OldExists {
		// 写入前全文（上限内）随账本落盘：供撤回与审计（超限时由 note 说明）
		payload["old_exists"] = true
		payload["old_content"] = res.OldContent
	}
	if _, aerr := ledger.Append(session.EventUserEdit, payload); aerr != nil {
		return out, fmt.Errorf("写入成功，但落账失败（这张卡重启后会丢失）：%w", aerr)
	}
	return out, nil
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
