package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// 2026-10-19：本步的带签名思考块回填到助手(tool_calls)消息，并在重折叠（从账本
// 重新派生）之后仍然在——thinking + 工具时缺了它们第二步请求就 400。
func TestAgent_ThinkingBlocksSurviveRefold(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	big := strings.Repeat("x", 40<<10)
	registry := tools.NewRegistry()
	if err := registry.Register(&scriptTool{name: "fs", result: tools.ToolResult{Content: big}}); err != nil {
		t.Fatal(err)
	}
	blk := &llm.ThinkingBlock{Thinking: "想", Signature: "SIG"}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{ThinkingBlock: blk}, {ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{"action":"read","path":"a"}`}}}, {EndReason: llm.EndDone}},
		{{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c2", Name: "fs", ArgumentsDelta: `{"action":"read","path":"b"}`}}}, {EndReason: llm.EndDone}},
		{{Delta: "完"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)
	loop.SetContextBudget(4000, false) // 每步都超水位 → 段内重折叠（从账本重新派生）
	ch, err := loop.Run(context.Background(), ledger, "读")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)
	for i := 1; i <= 2; i++ {
		found := false
		for _, m := range fr.reqs[i].Messages {
			if m.Role == "assistant" && len(m.ToolCalls) > 0 && m.ToolCalls[0].ID == "c1" {
				found = len(m.ThinkingBlocks) == 1 && m.ThinkingBlocks[0].Signature == "SIG"
			}
		}
		if !found {
			t.Fatalf("第 %d 次请求里 c1 的助手消息应带签名思考块", i+1)
		}
	}
}

// 本轮用量合计落 TurnEnd 账本（上游报了才有）；不报用量时载荷与旧版一致。
func TestAgent_TurnUsagePersisted(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "hi"}, {Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 7, TotalTokens: 107, CacheReadTokens: 80}}, {EndReason: llm.EndDone}},
	}}
	ch, err := NewLoop(fr, "m", nil).Run(context.Background(), ledger, "q")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)
	var turnEnd string
	if err := ledger.Replay(func(ev session.Event) error {
		if ev.Kind() == session.EventTurnEnd {
			turnEnd = string(ev.Data())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"prompt":100`, `"completion":7`, `"cache_read":80`} {
		if !strings.Contains(turnEnd, want) {
			t.Fatalf("TurnEnd 应带用量 %s：%s", want, turnEnd)
		}
	}
}
