package app

import (
	"encoding/json"

	"tiancode/internal/core/session"
)

// SessionUsageSummary 是一场对话累计的 token 用量（2026-10-19）。
// 数据源是每轮 TurnEnd 账本里的 usage（上游报了才有）：此前适配器解析出的用量
// 全部被丢弃——看不到花费，也看不到 prompt cache 是否命中。
// Turns 是带用量的轮数；ReportedAll=false 表示有完成的轮次没报用量（数字是下限）。
type SessionUsageSummary struct {
	PromptTokens     int64 `json:"promptTokens"`
	CompletionTokens int64 `json:"completionTokens"`
	TotalTokens      int64 `json:"totalTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
	Turns            int   `json:"turns"`
	ReportedAll      bool  `json:"reportedAll"`
}

// SessionUsage 汇总一场对话的 token 用量（只读扫描账本）。
func (s *ChatService) SessionUsage(sessionID string) (SessionUsageSummary, error) {
	ledger, err := s.ledgerFor(sessionID)
	if err != nil {
		return SessionUsageSummary{}, err
	}
	var sum SessionUsageSummary
	ended := 0
	err = ledger.Replay(func(ev session.Event) error {
		if ev.Kind() != session.EventTurnEnd {
			return nil
		}
		ended++
		var p struct {
			Usage *struct {
				Prompt     int64 `json:"prompt"`
				Completion int64 `json:"completion"`
				Total      int64 `json:"total"`
				CacheRead  int64 `json:"cache_read"`
				CacheWrite int64 `json:"cache_write"`
			} `json:"usage"`
		}
		if json.Unmarshal(ev.Data(), &p) != nil || p.Usage == nil {
			return nil
		}
		sum.Turns++
		sum.PromptTokens += p.Usage.Prompt
		sum.CompletionTokens += p.Usage.Completion
		sum.TotalTokens += p.Usage.Total
		sum.CacheReadTokens += p.Usage.CacheRead
		sum.CacheWriteTokens += p.Usage.CacheWrite
		return nil
	})
	if err != nil {
		return SessionUsageSummary{}, err
	}
	sum.ReportedAll = sum.Turns == ended
	return sum, nil
}
