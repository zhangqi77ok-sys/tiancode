package openaiprovider

import (
	"context"
	"os"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// TestSmoke_RealUpstream 是环境变量门控的真实上游冒烟测试（不入常规 CI）。
// 设置以下环境变量后才会运行，密钥永不入库（docs/STANDARDS.md §1）：
//
//	TIANCODE_SMOKE_BASEURL  例如 https://example.com/v1
//	TIANCODE_SMOKE_APIKEY   供应商密钥
//	TIANCODE_SMOKE_MODEL    模型名
//
// 目的：用真实网关验证 httptest 覆盖不到的现实行为——真实 SSE 分片粒度、
// 思考流字段、网关心跳、代理层超时等。
func TestSmoke_RealUpstream(t *testing.T) {
	base := os.Getenv("TIANCODE_SMOKE_BASEURL")
	key := os.Getenv("TIANCODE_SMOKE_APIKEY")
	model := os.Getenv("TIANCODE_SMOKE_MODEL")
	if base == "" || key == "" || model == "" {
		t.Skip("smoke env not set (TIANCODE_SMOKE_BASEURL/APIKEY/MODEL)")
	}

	p := New(Options{
		BaseURL:     base,
		APIKey:      key,
		IdleTimeout: 60 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	ch, err := p.StreamChat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: "user", Content: "只回复两个字：你好"},
		},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var text string
	var terminal llm.StreamChunk
	deadline := time.After(85 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				if terminal.EndReason != llm.EndDone {
					t.Fatalf("stream closed, terminal = %+v, want EndDone", terminal)
				}
				t.Logf("smoke OK: %d chars, model replied: %q", len(text), text)
				return
			}
			if c.Delta != "" {
				text += c.Delta
			}
			if c.EndReason != llm.EndNone {
				terminal = c
				if c.EndReason != llm.EndDone {
					t.Fatalf("terminal = %+v (err=%v), want EndDone", c.EndReason, c.Err)
				}
			}
		case <-deadline:
			t.Fatalf("smoke timed out; collected %d chars so far", len(text))
		}
	}
}
