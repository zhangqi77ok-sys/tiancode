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

func toolMsg(t *testing.T, req llm.ChatRequest, callID string) string {
	t.Helper()
	for _, m := range req.Messages {
		if m.Role == "tool" && m.ToolCallID == callID {
			return m.Content
		}
	}
	t.Fatalf("请求里没有 %s 的工具结果", callID)
	return ""
}

func callStep(id string) []llm.StreamChunk {
	return []llm.StreamChunk{
		{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: id, Name: "fs", ArgumentsDelta: `{"action":"read","path":"big.txt"}`}}},
		{EndReason: llm.EndDone},
	}
}

// P1：单条工具结果进入本回合上下文前有上限（此前 10MB 的 read 原样进请求）；账本仍存全文。
func TestAgent_InTurnToolResultCapped(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	huge := strings.Repeat("y", 300<<10)
	registry := tools.NewRegistry()
	if err := registry.Register(&scriptTool{name: "fs", result: tools.ToolResult{Content: huge}}); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		callStep("c1"),
		{{Delta: "ok"}, {EndReason: llm.EndDone}},
	}}
	ch, err := NewLoop(fr, "m", registry).Run(context.Background(), ledger, "读大文件")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)

	got := toolMsg(t, fr.reqs[1], "c1")
	if len(got) > inTurnToolResultLimit+512 || !strings.Contains(got, "本回合视图已截断") {
		t.Fatalf("回合内工具结果应按上限截断并注明：len=%d", len(got))
	}
	// 账本全文（ADR-0008）
	full := ""
	if err := ledger.Replay(func(ev session.Event) error {
		if ev.Kind() == session.EventToolResult && strings.Contains(string(ev.Data()), `"c1"`) {
			full = string(ev.Data())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(full) < len(huge) {
		t.Fatalf("账本应保留全文：len=%d", len(full))
	}
}

// P1：段内超预算水位 → 从账本重新派生（更早步骤的结果被截到模型侧上限），
// 本步刚产生的结果原样保留；此前段内上下文只增不减，直到上游拒收。
func TestAgent_MidTurnRefoldKeepsFreshResult(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	body := strings.Repeat("x", 40<<10)
	registry := tools.NewRegistry()
	if err := registry.Register(&scriptTool{name: "fs", result: tools.ToolResult{Content: body}}); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		callStep("c1"),
		callStep("c2"),
		{{Delta: "done"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)
	loop.SetContextBudget(4000, false)
	ch, err := loop.Run(context.Background(), ledger, "连读两次")
	if err != nil {
		t.Fatal(err)
	}
	var sawContext bool
	var last llm.StreamChunk
	for _, c := range drain(t, ch, 5*time.Second) {
		if c.Context != nil {
			sawContext = true
		}
		if c.EndReason != llm.EndNone {
			last = c
		}
	}
	if last.EndReason != llm.EndDone {
		t.Fatalf("want EndDone, got %v err=%v", last.EndReason, last.Err)
	}
	if len(fr.reqs) != 3 {
		t.Fatalf("model calls = %d, want 3（单回合内不应触发压缩调用）", len(fr.reqs))
	}
	if got := toolMsg(t, fr.reqs[1], "c1"); got != body {
		t.Fatalf("第 2 次请求里 c1 是本步结果，应原样保留：len=%d", len(got))
	}
	if got := toolMsg(t, fr.reqs[2], "c1"); len(got) > toolResultModelLimit+256 {
		t.Fatalf("第 3 次请求里 c1 已是更早步骤，应被重折叠截断：len=%d", len(got))
	}
	if got := toolMsg(t, fr.reqs[2], "c2"); got != body {
		t.Fatalf("第 3 次请求里 c2 是本步结果，应原样保留：len=%d", len(got))
	}
	if !sawContext {
		t.Fatal("重折叠应上报油表读数（折叠绝不静默）")
	}
}

// P1：未超水位时不重派生——请求里保留回合内全文（零行为变化）。
func TestAgent_MidTurnNoRefoldUnderBudget(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	body := strings.Repeat("z", 8<<10)
	registry := tools.NewRegistry()
	if err := registry.Register(&scriptTool{name: "fs", result: tools.ToolResult{Content: body}}); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		callStep("c1"), callStep("c2"),
		{{Delta: "done"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)
	loop.SetContextBudget(200000, false)
	ch, err := loop.Run(context.Background(), ledger, "读两次")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)
	if got := toolMsg(t, fr.reqs[2], "c1"); got != body {
		t.Fatalf("未超水位不应截断更早步骤的结果：len=%d", len(got))
	}
}

// P1：压缩可以多次发生（此前一个会话只能压缩一次，第二次超限永久硬报错）；
// 第二次的摘要输入要并入第一次的摘要。
func TestAgent_CompactionCanRepeat(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()
	big := strings.Repeat("背景讨论。", 20)
	addTurns := func(from, n int) {
		for i := from; i < from+n; i++ {
			if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": big + "（轮" + string(rune('A'+i)) + "）"}); err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": "收到"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	run := func(fr *fakeRuntime, text string) {
		loop := NewLoop(fr, "m", nil)
		loop.SetContextBudget(500, false)
		ch, err := loop.Run(context.Background(), ledger, text)
		if err != nil {
			t.Fatalf("应压缩后继续，仍报错：%v", err)
		}
		drain(t, ch, 5*time.Second)
	}

	addTurns(0, 6)
	run(&fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "第一段摘要：早期讨论了背景。"}, {EndReason: llm.EndDone}},
		{{Delta: "继续"}, {EndReason: llm.EndDone}},
	}}, big+"（第七轮）")

	addTurns(10, 4)
	fr2 := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "第二段摘要。"}, {EndReason: llm.EndDone}},
		{{Delta: "继续"}, {EndReason: llm.EndDone}},
	}}
	run(fr2, big+"（第十二轮）")

	if n := countEvents(t, dir, session.EventCompaction); n != 2 {
		t.Fatalf("compaction events = %d, want 2", n)
	}
	if len(fr2.reqs) != 2 || !strings.Contains(fr2.reqs[0].Messages[1].Content, "第一段摘要") {
		t.Fatalf("第二次摘要的转录应包含第一次的摘要：%+v", fr2.reqs)
	}
	if !strings.Contains(fr2.reqs[1].Messages[0].Content, "第二段摘要") {
		t.Fatalf("正式请求应以最新摘要开头：%q", fr2.reqs[1].Messages[0].Content)
	}
}
