package telemetry

import (
	"strings"
	"testing"
)

func TestTracker_EstimatedCostAccuracy(t *testing.T) {
	tracker := NewTracker()

	// 初始状态
	m := tracker.GetMetrics(1)
	if m.TotalTokens != 0 {
		t.Errorf("expected 0 tokens, got %d", m.TotalTokens)
	}
	if m.EstimatedCost != "$0.0000" {
		t.Errorf("expected $0.0000, got %s", m.EstimatedCost)
	}

	// 记录调用 (1000 prompt + 500 comp = 1500 tokens)
	tracker.Record("deepseek-chat", 1000, 500, 120)
	m2 := tracker.GetMetrics(1)
	if m2.TotalTokens != 1500 {
		t.Errorf("expected 1500 tokens, got %d", m2.TotalTokens)
	}
	// 1500 * 0.000002 = 0.0030
	if m2.EstimatedCost != "$0.0030" {
		t.Errorf("expected $0.0030, got %s", m2.EstimatedCost)
	}
	if strings.Contains(m2.EstimatedCost, ":") {
		t.Errorf("EstimatedCost should not contain time colon: %s", m2.EstimatedCost)
	}
}

func TestTracker_EdgeDefenses(t *testing.T) {
	tracker := NewTracker()

	// 1. 空模型名称自动归一化为 "unknown"
	tracker.Record("   ", 100, 50, 10)
	m := tracker.GetMetrics(-5) // 负数 activeSessions 防护
	if m.ActiveSessions != 0 {
		t.Errorf("expected activeSessions clamped to 0, got %d", m.ActiveSessions)
	}
	if _, ok := m.PerModel["unknown"]; !ok {
		t.Errorf("expected 'unknown' model key for empty model name")
	}

	// 2. 负数 tokens 与耗时防御
	tracker.Record("test-model", -50, -20, -100)
	m2 := tracker.GetMetrics(1)
	mu := m2.PerModel["test-model"]
	if mu.TotalTokens < 0 || mu.AvgLatencyMs < 0 {
		t.Errorf("expected non-negative values for negative token input")
	}
}

func TestTracker_KVCacheMetrics(t *testing.T) {
	tracker := NewTracker()

	// 模拟首轮: 10,000 prompt tokens, 100 completion tokens, 0 cache read, 10,000 cache write
	tracker.RecordWithCache("claude-3-7-sonnet", 10000, 100, 0, 10000, 1200)

	m1 := tracker.GetMetrics(1)
	if m1.CacheHitRate != 0.0 {
		t.Errorf("expected 0.0 cache hit rate on turn 1, got %f", m1.CacheHitRate)
	}
	if m1.CachedTokens != 0 {
		t.Errorf("expected 0 cached tokens on turn 1, got %d", m1.CachedTokens)
	}

	// 模拟第二轮: 12,000 prompt tokens, 其中 10,000 为 cache read (命中前缀), 200 completion tokens
	tracker.RecordWithCache("claude-3-7-sonnet", 12000, 200, 10000, 2000, 450)

	m2 := tracker.GetMetrics(1)
	// 累计 prompt = 22,000, 累计 cache read = 10,000 -> 10,000 / 22,000 ≈ 45.45%
	expectedHitRate := (10000.0 / 22000.0) * 100.0
	if m2.CacheHitRate < expectedHitRate-0.1 || m2.CacheHitRate > expectedHitRate+0.1 {
		t.Errorf("expected hit rate around %.2f, got %.2f", expectedHitRate, m2.CacheHitRate)
	}
	if m2.CacheReadTokens != 10000 {
		t.Errorf("expected 10000 cache read tokens, got %d", m2.CacheReadTokens)
	}
	// 10000 * 0.0000018 = 0.0180
	if m2.CacheSavingsUSD != "$0.0180" {
		t.Errorf("expected $0.0180 savings, got %s", m2.CacheSavingsUSD)
	}
}

