package app

import (
	"encoding/json"
	"fmt"

	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// 回合检查点服务（第 6 批）：撤回本轮 / 从这条用户消息重跑。
//
// 事实源纪律：检查点在轮次收尾时由 sendCore 写入账本（EventRoundCheckpoint）；
// 撤回与重跑都是"追加新事件"（EventRoundRevert / EventFork），旧行永不改写。

// RevertResult 是「撤回本轮」的结果。
type RevertResult struct {
	Round    int      `json:"round"`    // 撤回的是第几轮用户轮次（1 起）
	Restored []string `json:"restored"` // 已恢复的文件
	Skipped  []string `json:"skipped"`  // 撤不回的文件（含原因）
}

// RerunResult 是「从这条用户消息重跑」的结果（前端据 Text 重新发送）。
type RerunResult struct {
	Text     string   `json:"text"`     // 原用户消息文本
	Reverted []string `json:"reverted"` // 已按检查点撤回的文件
	Skipped  []string `json:"skipped"`  // 撤不回的文件（含原因）
}

// roundCPRecord 是账本里一条轮次检查点（含它属于第几轮）。
type roundCPRecord struct {
	seq   int64
	round int
	files []tools.RoundCheckpoint
}

// lastRevertableRound 找最后一个未被撤回的轮次检查点（跳过分叉丢弃区间）。
func (s *ChatService) lastRevertableRound(ledger *session.Ledger) (*roundCPRecord, error) {
	drops, err := ledger.ForkDrops()
	if err != nil {
		return nil, err
	}
	turn := 0
	var cps []roundCPRecord
	reverted := map[int64]bool{} // 已被 EventRoundRevert 消费的检查点 seq
	if err := ledger.Replay(func(ev session.Event) error {
		if session.ForkDropped(drops, ev.Seq()) {
			return nil
		}
		switch ev.Kind() {
		case session.EventUserMessage:
			turn++
		case session.EventRoundCheckpoint:
			var p struct {
				Files []tools.RoundCheckpoint `json:"files"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			cps = append(cps, roundCPRecord{seq: ev.Seq(), round: turn, files: p.Files})
		case session.EventRoundRevert:
			var p struct {
				CheckpointSeq int64 `json:"checkpoint_seq"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			reverted[p.CheckpointSeq] = true
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for i := len(cps) - 1; i >= 0; i-- {
		if !reverted[cps[i].seq] {
			return &cps[i], nil
		}
	}
	return nil, nil
}

// RevertRound 撤回最近一个（未被撤回的）轮次：按检查点恢复该轮改过的文件。
// 纪律：文件在本轮之后被改过（哈希不符）或检查点超限时**跳过并明确报告**——
// 绝不覆盖用户改动、绝不假装能撤。撤回动作落账本（EventRoundRevert）。
func (s *ChatService) RevertRound(sessionID string) (RevertResult, error) {
	// 存活校验必须在 ledgerFor（会给已删除会话重建空文件）之前：权威判据是
	// 账本文件还在盘上（第二轮体检 R3——绝不给已删除会话重建无主工具集）。
	if !s.sessionLedgerOnDisk(sessionID) {
		return RevertResult{}, fmt.Errorf("会话不存在或已删除：%s", sessionID)
	}
	ledger, err := s.ledgerFor(sessionID)
	if err != nil {
		return RevertResult{}, err
	}
	root := s.sessionWorkspace(sessionID)
	if root == "" {
		return RevertResult{}, fmt.Errorf("这场对话没有工作区，无法撤回文件")
	}
	st, err := s.ensureSessionTools(sessionID, root)
	if err != nil {
		return RevertResult{}, err
	}
	rec, err := s.lastRevertableRound(ledger)
	if err != nil {
		return RevertResult{}, err
	}
	if rec == nil {
		return RevertResult{}, fmt.Errorf("没有可撤回的轮次（本轮没有文件改动，或已撤回）")
	}
	res := restoreRoundCheckpoints(st, rec.files)
	res.Round = rec.round
	// 撤回动作落账本（含结果：哪些恢复、哪些跳过）——同时标记该检查点已消费
	if _, err := ledger.Append(session.EventRoundRevert, map[string]any{
		"checkpoint_seq": rec.seq,
		"round":          rec.round,
		"restored":       res.Restored,
		"skipped":        res.Skipped,
	}); err != nil {
		return res, fmt.Errorf("撤回已执行但落账失败：%w", err)
	}
	return res, nil
}

// restoreRoundCheckpoints 按检查点逐文件恢复：失败的进 Skipped（含原因），绝不静默。
func restoreRoundCheckpoints(st *sessionTools, files []tools.RoundCheckpoint) RevertResult {
	res := RevertResult{Restored: []string{}, Skipped: []string{}}
	if st == nil || st.fs == nil {
		for _, cp := range files {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s：无文件工具（纯对话会话）", cp.Path))
		}
		return res
	}
	for _, cp := range files {
		if err := st.fs.RestoreCheckpoint(cp); err != nil {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s：%v", cp.Path, err))
			continue
		}
		res.Restored = append(res.Restored, cp.Path)
	}
	return res
}

// RerunFrom 从指定的用户消息重跑（第 6 批）：
//  1. 撤回该消息之后所有轮次的文件改动（按检查点；同文件取最早的一份 = 该消息时的状态）；
//  2. 追加 EventFork{from_seq}：派生/投影丢弃 [from_seq, fork] 的旧历史——账本只追加、
//     旧行不改（用户要求"不要悄悄改旧行"）；目标 user 消息本身也在丢弃范围内，
//     重跑会重新落一条同文本消息（否则模型会看到两条重复输入）；
//  3. 返回原文，由调用方（前端）重新 Send。
//
// 撤不回的文件在 Skipped 里明确列出（绝不静默）；纯对话会话（无工作区）照常可重跑。
func (s *ChatService) RerunFrom(sessionID string, userSeq int64) (RerunResult, error) {
	ledger, err := s.ledgerFor(sessionID)
	if err != nil {
		return RerunResult{}, err
	}
	drops, err := ledger.ForkDrops()
	if err != nil {
		return RerunResult{}, err
	}
	// 校验目标：必须是账本里（未被分叉丢弃的）一条 user_message
	var text string
	found := false
	if err := ledger.Replay(func(ev session.Event) error {
		if session.ForkDropped(drops, ev.Seq()) || ev.Seq() != userSeq || ev.Kind() != session.EventUserMessage {
			return nil
		}
		var p struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err != nil {
			return err
		}
		text = p.Text
		found = true
		return nil
	}); err != nil {
		return RerunResult{}, err
	}
	if !found {
		return RerunResult{}, fmt.Errorf("找不到序号为 %d 的用户消息（可能已被重跑丢弃）", userSeq)
	}
	// 收集该消息之后的检查点（按时间序），同文件保留最早的一份
	var order []string
	byPath := map[string]tools.RoundCheckpoint{}
	if err := ledger.Replay(func(ev session.Event) error {
		if session.ForkDropped(drops, ev.Seq()) || ev.Seq() <= userSeq || ev.Kind() != session.EventRoundCheckpoint {
			return nil
		}
		var p struct {
			Files []tools.RoundCheckpoint `json:"files"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err != nil {
			return err
		}
		for _, cp := range p.Files {
			if _, ok := byPath[cp.Path]; ok {
				continue // 保留最早的（= 该用户消息时的状态）
			}
			byPath[cp.Path] = cp
			order = append(order, cp.Path)
		}
		return nil
	}); err != nil {
		return RerunResult{}, err
	}
	res := RerunResult{Text: text, Reverted: []string{}, Skipped: []string{}}
	if len(order) > 0 {
		files := make([]tools.RoundCheckpoint, 0, len(order))
		for _, p := range order {
			files = append(files, byPath[p])
		}
		root := s.sessionWorkspace(sessionID)
		if root == "" {
			res.Skipped = append(res.Skipped, "（无工作区，文件改动未撤回）")
		} else {
			st, err := s.ensureSessionTools(sessionID, root)
			if err != nil {
				return RerunResult{}, err
			}
			rr := restoreRoundCheckpoints(st, files)
			res.Reverted, res.Skipped = rr.Restored, rr.Skipped
		}
	}
	// 分叉：丢弃 [userSeq, fork] 的旧历史（目标消息本身也在内）
	if _, err := ledger.Append(session.EventFork, map[string]any{"from_seq": userSeq, "reason": "rerun"}); err != nil {
		return res, fmt.Errorf("撤回已完成但分叉落账失败：%w", err)
	}
	return res, nil
}
