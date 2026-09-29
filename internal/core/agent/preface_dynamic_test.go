package agent

import (
	"context"
	"encoding/json"
	"testing"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/tools"
)

// 动态 preface（0.2.33）：扩展在回合内被增删后，后续步骤的模型请求必须
// 立即看到最新清单——静态 preface 只在回合开始固定，"添加当回合不可用"。
func TestLoop_DynamicPrefaceRefreshesEachStep(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "t1", Name: "noop", ArgumentsDelta: `{}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "done"}, {EndReason: llm.EndDone}},
	}}
	registry := tools.NewRegistry()
	if err := registry.Register(&noopTool{}); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(fr, "m", registry)

	// 用调用次数驱动（turn 是 goroutine，外部改闭包变量有竞争）：
	// 第 1 步读 v1，第 2 步读 v2——模拟"第 1 步期间 ManageTool 保存了新配置"
	calls := 0
	loop.SetPrefaceFn(func() string {
		calls++
		if calls == 1 {
			return "清单 v1"
		}
		return "清单 v2（技能已添加）"
	})

	ch, err := loop.Run(context.Background(), ledger, "hi")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}

	if len(fr.reqs) != 2 {
		t.Fatalf("请求数 = %d, want 2", len(fr.reqs))
	}
	first := systemOf(fr.reqs[0])
	second := systemOf(fr.reqs[1])
	if first != "清单 v1" {
		t.Fatalf("第 1 步 system = %q, want 清单 v1", first)
	}
	if second != "清单 v2（技能已添加）" {
		t.Fatalf("第 2 步 system = %q, want 第 2 步看到最新清单", second)
	}
}

func systemOf(req llm.ChatRequest) string {
	for _, m := range req.Messages {
		if m.Role == "system" {
			return m.Content
		}
	}
	return ""
}

type noopTool struct{}

func (n *noopTool) Name() string            { return "noop" }
func (n *noopTool) Description() string     { return "noop" }
func (n *noopTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (n *noopTool) Execute(_ context.Context, _ json.RawMessage) (tools.ToolResult, error) {
	return tools.ToolResult{Content: "ok"}, nil
}
