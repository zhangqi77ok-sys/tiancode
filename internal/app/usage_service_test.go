package app

import (
	"testing"

	"tiancode/internal/core/session"
)

// 2026-10-19：会话用量 = 各轮 TurnEnd 用量之和；有轮次没报用量时 reportedAll=false。
func TestSessionUsage_SumsTurnEnds(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	l, err := s.ledgerFor("s-usage")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []map[string]any{
		{"reason": "done", "usage": map[string]int64{"prompt": 100, "completion": 10, "total": 110, "cache_read": 60}},
		{"reason": "done", "usage": map[string]int64{"prompt": 200, "completion": 20, "total": 220, "cache_write": 5}},
		{"reason": "done"},
	} {
		if _, err := l.Append(session.EventTurnEnd, p); err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.SessionUsage("s-usage")
	if err != nil {
		t.Fatal(err)
	}
	if u.PromptTokens != 300 || u.CompletionTokens != 30 || u.TotalTokens != 330 || u.CacheReadTokens != 60 || u.CacheWriteTokens != 5 || u.Turns != 2 || u.ReportedAll {
		t.Fatalf("用量汇总不符：%+v", u)
	}
}
