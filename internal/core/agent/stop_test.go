package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/tools"
)

// blockingTool：阻塞直到 ctx 取消（模拟执行中的长命令被停止打断）。
type blockingTool struct {
	name     string
	released atomicFlag
}

type atomicFlag struct{ v int32 }

func (b *blockingTool) Name() string        { return b.name }
func (b *blockingTool) Description() string { return "blocks until ctx cancel" }
func (b *blockingTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)
}

func (b *blockingTool) Execute(ctx context.Context, _ json.RawMessage) (tools.ToolResult, error) {
	b.released.set()
	<-ctx.Done() // 阻塞到取消：Execute 必须感知会话取消并快速返回
	return tools.ToolResult{Content: "已中断", IsError: true}, nil
}

func (f *atomicFlag) set() { f.v = 1 }

// 0.0.10：停止必须打断当前工具——Execute 感知取消后尽快返回（不等命令自然结束）；
// 被中断之后本轮剩余工具不被调用、不再请求模型；终态 EndCancelled。
func TestAgent_StopInterruptsCurrentTool(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	blocker := &blockingTool{name: "shell"}
	after := &slowTool{name: "fs", delay: 50 * time.Millisecond}
	after.peak = &sharedPeak{}
	registry := tools.NewRegistry()
	for _, tl := range []tools.ToolPort{blocker, after} {
		if err := registry.Register(tl); err != nil {
			t.Fatal(err)
		}
	}
	fr := &fakeRuntime{script: scriptCalls(
		llm.ToolCallChunk{Index: 0, ID: "c1", Name: "shell", ArgumentsDelta: `{"cmd":"sleep 120"}`},
		llm.ToolCallChunk{Index: 1, ID: "c2", Name: "fs", ArgumentsDelta: `{"action":"read"}`},
	)}
	loop := NewLoop(fr, "test-model", registry)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := loop.Run(ctx, ledger, "long run")
	if err != nil {
		t.Fatal(err)
	}

	// 1 秒内点停止（模拟人按停止键）
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	var terminal llm.StreamChunk
	var seen []string
	for c := range ch {
		if c.EndReason != llm.EndNone {
			terminal = c
			seen = append(seen, "terminal")
		} else if c.ToolEvent != nil {
			seen = append(seen, c.ToolEvent.Name+":"+c.ToolEvent.Status)
		} else if c.Delta != "" {
			seen = append(seen, "delta:"+c.Delta)
		}
	}
	elapsed := time.Since(start)
	t.Logf("seen=%v", seen)

	if terminal.EndReason != llm.EndCancelled {
		t.Fatalf("终态 = %v, want EndCancelled", terminal.EndReason)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("取消后必须尽快返回（不等命令结束），实测 %v", elapsed)
	}
	_ = blocker.released
	// 后续工具不被调用：fs 的执行计数为 0（peak.enter 未发生）
	if after.peak.peak.Load() != 0 {
		t.Fatalf("被中断之后剩余工具不得被调用，fs 执行峰值 = %d", after.peak.peak.Load())
	}
	// 不再请求模型：只有第一步入账（requests = 1）
	if fr.requestCount() != 1 {
		t.Fatalf("requests = %d, want 1（取消后不得再请求模型）", fr.requestCount())
	}
}
