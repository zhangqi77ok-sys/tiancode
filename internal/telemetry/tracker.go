package telemetry

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ModelUsage 单个模型的累计用量统计
type ModelUsage struct {
	Model               string  `json:"model"`
	Calls               int     `json:"calls"`
	TotalTokens         int     `json:"total_tokens"`
	PromptTokens        int     `json:"prompt_tokens"`
	CompTokens          int     `json:"comp_tokens"`
	CachedTokens        int     `json:"cached_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
	CacheHitRate        float64 `json:"cache_hit_rate"` // 0.0 ~ 100.0%
	AvgLatencyMs        int64   `json:"avg_latency_ms"`
}

// UsageMetrics 全局 Token 消耗与网关遥测大盘
type UsageMetrics struct {
	TotalTokens         int                   `json:"total_tokens"`
	TotalCalls          int                   `json:"total_calls"`
	EstimatedCost       string                `json:"estimated_cost"` // 美元预估 (如 "$0.0420")
	CachedTokens        int                   `json:"cached_tokens"`
	CacheReadTokens     int                   `json:"cache_read_tokens"`
	CacheCreationTokens int                   `json:"cache_creation_tokens"`
	CacheHitRate        float64               `json:"cache_hit_rate"`        // 全局前缀缓存命中率 0.0 ~ 100.0%
	CacheSavingsUSD     string                `json:"cache_savings_usd"`     // 已省金额 (如 "$1.2050")
	ActiveSessions      int                   `json:"active_sessions"`
	PerModel            map[string]ModelUsage `json:"per_model"`
	LastUpdatedTime     string                `json:"last_updated_time"`
}

type Tracker struct {
	mu       sync.RWMutex
	perModel map[string]*ModelUsage
}

var globalTracker = NewTracker()

func NewTracker() *Tracker {
	return &Tracker{
		perModel: make(map[string]*ModelUsage),
	}
}

func GetTracker() *Tracker {
	return globalTracker
}

func (t *Tracker) Record(model string, promptTokens, compTokens int, durationMs int64) {
	t.RecordWithCache(model, promptTokens, compTokens, 0, 0, durationMs)
}

func (t *Tracker) RecordWithCache(model string, promptTokens, compTokens, cacheReadTokens, cacheCreationTokens int, durationMs int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	trimmedModel := strings.TrimSpace(model)
	if trimmedModel == "" {
		trimmedModel = "unknown"
	}
	if promptTokens < 0 {
		promptTokens = 0
	}
	if compTokens < 0 {
		compTokens = 0
	}
	if cacheReadTokens < 0 {
		cacheReadTokens = 0
	}
	if cacheCreationTokens < 0 {
		cacheCreationTokens = 0
	}
	if durationMs < 0 {
		durationMs = 0
	}

	mu, exists := t.perModel[trimmedModel]
	if !exists {
		mu = &ModelUsage{
			Model: trimmedModel,
		}
		t.perModel[trimmedModel] = mu
	}

	mu.Calls++
	mu.PromptTokens += promptTokens
	mu.CompTokens += compTokens
	mu.TotalTokens += (promptTokens + compTokens)
	mu.CacheReadTokens += cacheReadTokens
	mu.CacheCreationTokens += cacheCreationTokens
	mu.CachedTokens += cacheReadTokens
	if mu.PromptTokens > 0 {
		mu.CacheHitRate = float64(mu.CacheReadTokens) / float64(mu.PromptTokens) * 100.0
	}
	if mu.Calls > 0 {
		mu.AvgLatencyMs = (mu.AvgLatencyMs*int64(mu.Calls-1) + durationMs) / int64(mu.Calls)
	}
}

func (t *Tracker) GetMetrics(activeSessions int) UsageMetrics {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if activeSessions < 0 {
		activeSessions = 0
	}

	totalTokens := 0
	totalCalls := 0
	totalPromptTokens := 0
	totalCacheRead := 0
	totalCacheCreate := 0
	resMap := make(map[string]ModelUsage)

	for m, u := range t.perModel {
		totalTokens += u.TotalTokens
		totalCalls += u.Calls
		totalPromptTokens += u.PromptTokens
		totalCacheRead += u.CacheReadTokens
		totalCacheCreate += u.CacheCreationTokens
		resMap[m] = *u
	}

	// 预估成本估算 (平均 $0.002 / 1k tokens)
	cost := float64(totalTokens) * 0.000002
	costStr := fmt.Sprintf("$%.4f", cost)

	// KV Cache 节省金额：按读取缓存享受 ~80-90% 折扣折算 (平均省 $0.0018 / 1k cached tokens)
	savings := float64(totalCacheRead) * 0.0000018
	savingsStr := fmt.Sprintf("$%.4f", savings)

	globalHitRate := 0.0
	if totalPromptTokens > 0 {
		globalHitRate = float64(totalCacheRead) / float64(totalPromptTokens) * 100.0
	}

	return UsageMetrics{
		TotalTokens:         totalTokens,
		TotalCalls:          totalCalls,
		EstimatedCost:       costStr,
		CachedTokens:        totalCacheRead,
		CacheReadTokens:     totalCacheRead,
		CacheCreationTokens: totalCacheCreate,
		CacheHitRate:        globalHitRate,
		CacheSavingsUSD:     savingsStr,
		ActiveSessions:      activeSessions,
		PerModel:            resMap,
		LastUpdatedTime:     time.Now().Format("15:04:05"),
	}
}
