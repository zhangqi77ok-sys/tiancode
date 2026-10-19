package subagenttool

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/tools"
)

// 2026-10-19：报告只取最终答复——此前把每一步调工具前的中间叙述也拼进报告。
func TestSubagent_ReportIsFinalAnswerOnly(t *testing.T) {
	fs := &scriptTool{name: "fs", result: tools.ToolResult{Content: "x", Title: "a.go"}}
	rt := &fakeRuntime{file: [][]llm.StreamChunk{
		{{Delta: "我先读一下 a.go。"}, toolCall(0, "fs", `{"action":"read","path":"a.go"}`), {EndReason: llm.EndDone}},
		{{Delta: "结论：a.go 只有 x。"}, {EndReason: llm.EndDone}},
	}}
	res := run(t, New(rt, "m", fs), `{"prompt":"调研 a.go"}`)
	if res.IsError || res.Content != "结论：a.go 只有 x。" {
		t.Fatalf("报告应只含最终答复：%q", res.Content)
	}
}

// 过程经调用级回调（ctx）推给主对话的工具卡：工具步骤可见，不再 15 分钟黑盒。
func TestSubagent_ProgressViaContext(t *testing.T) {
	fs := &scriptTool{name: "fs", result: tools.ToolResult{Content: "x", Title: "a.go"}}
	rt := &fakeRuntime{file: [][]llm.StreamChunk{
		{toolCall(0, "fs", `{"action":"read","path":"a.go"}`), {EndReason: llm.EndDone}},
		{{Delta: "完成"}, {EndReason: llm.EndDone}},
	}}
	var mu sync.Mutex
	var got []string
	ctx := tools.WithProgress(context.Background(), func(p string) {
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
	})
	res, err := New(rt, "m", fs).Execute(ctx, json.RawMessage(`{"prompt":"调研"}`))
	if err != nil || res.IsError {
		t.Fatalf("不应失败：%v %+v", err, res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 || !strings.Contains(got[len(got)-1], "已完成 1 次工具调用") || !strings.Contains(got[len(got)-1], "fs a.go") {
		t.Fatalf("应推送过程视图：%q", got)
	}
}

// 子代理带上下文预算与环境事实：大结果在后续步骤被重折叠（此前预算为 0 不折叠），
// 系统说明里有环境事实。
func TestSubagent_BudgetAndFacts(t *testing.T) {
	big := strings.Repeat("y", 40<<10)
	fs := &scriptTool{name: "fs", result: tools.ToolResult{Content: big, Title: "big.txt"}}
	rt := &fakeRuntime{file: [][]llm.StreamChunk{
		{toolCall(0, "fs", `{"action":"read","path":"big.txt"}`), {EndReason: llm.EndDone}},
		{toolCall(1, "fs", `{"action":"read","path":"big.txt"}`), {EndReason: llm.EndDone}},
		{{Delta: "完成"}, {EndReason: llm.EndDone}},
	}}
	tl := New(rt, "m", fs).WithContextBudget(4000, false).WithFacts("## 运行环境\n- 操作系统：Windows")
	if res := run(t, tl, `{"prompt":"读两次"}`); res.IsError {
		t.Fatalf("不应失败：%s", res.Content)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if !strings.Contains(rt.reqs[0].Messages[0].Content, "操作系统：Windows") {
		t.Fatalf("系统说明应带环境事实：%q", rt.reqs[0].Messages[0].Content)
	}
	for _, m := range rt.reqs[2].Messages {
		if m.Role == "tool" && m.ToolCallID == "c0" && len(m.Content) > 8<<10 {
			t.Fatalf("更早步骤的大结果应被重折叠：len=%d", len(m.Content))
		}
	}
}
