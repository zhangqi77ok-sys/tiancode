package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/tools"
)

// 阶段 4-1：清单过期提醒的状态机——"一张快照最多催一次，重交后归零"。
func TestTodoTrack_StateMachine(t *testing.T) {
	var tr todoTrack
	if got := tr.consumeRefresh(); got != "" {
		t.Fatalf("还没交过清单就不该提醒：%q", got)
	}
	tr.markWork()
	if got := tr.consumeRefresh(); got != "" {
		t.Fatalf("没交过清单时干活也不提醒：%q", got)
	}

	tr.observe([]llm.TodoItem{{Text: "a", Status: "pending"}})
	if got := tr.consumeRefresh(); got != "" {
		t.Fatalf("刚交完清单不该提醒：%q", got)
	}

	tr.markWork()
	first := tr.consumeRefresh()
	if first == "" {
		t.Fatal("交完清单又干过活：必须提醒重交")
	}
	// 提醒的身份与动作都要写清（它不是用户原话；动作是全量重交）
	if !strings.Contains(first, "不是用户原话") || !strings.Contains(first, "重新提交") {
		t.Fatalf("提醒要说清身份与动作：%q", first)
	}
	if again := tr.consumeRefresh(); again != "" {
		t.Fatalf("同一张快照不反复催：%q", again)
	}

	// 模型重交（新快照）→ 归零；再干活才再提醒一次
	tr.observe([]llm.TodoItem{{Text: "a", Status: "done"}})
	if got := tr.consumeRefresh(); got != "" {
		t.Fatalf("重交后不该立刻提醒：%q", got)
	}
	tr.markWork()
	if got := tr.consumeRefresh(); got == "" {
		t.Fatal("重交后又干过活 → 应再提醒一次")
	}
}

// 端到端：交清单 → 干活不回写 → 下一次请求里必须能看到重交提醒，且提醒不落账本。
func TestLoop_TodoRefreshReminderAfterWork(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	registry := tools.NewRegistry()
	if err := registry.Register(NewTodoTool()); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&scriptTool{name: "read", result: tools.ToolResult{Content: "ok"}}); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{ // 第 1 步：交清单（两项都还没完成）
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "t1", Name: "todo",
				ArgumentsDelta: `{"items":[{"text":"a","status":"pending"},{"text":"b","status":"pending"}]}`}}},
			{EndReason: llm.EndDone},
		},
		{ // 第 2 步：干活，但不回写清单
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c2", Name: "read", ArgumentsDelta: `{}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "做完了"}, {EndReason: llm.EndDone}}, // 第 3 步：最终答复
	}}
	loop := NewLoop(fr, "m", registry)

	ch, err := loop.Run(context.Background(), ledger, "干活")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)

	if len(fr.reqs) != 3 {
		t.Fatalf("请求数 = %d, want 3", len(fr.reqs))
	}
	if text := messagesText(fr.reqs[0].Messages); strings.Contains(text, "本轮提醒") {
		t.Fatalf("第一步不该有提醒（清单刚交）：%s", text)
	}
	third := messagesText(fr.reqs[2].Messages)
	if !strings.Contains(third, "本轮提醒") || !strings.Contains(third, "重新提交") {
		t.Fatalf("干过活却没重交清单：第三步请求里应能看到重交提醒：%s", third)
	}
	if ledgerContains(t, dir, "本轮提醒") {
		t.Fatal("提醒只存在于本轮内存：不得写进账本")
	}
}

// 重交后不再对旧快照啰嗦；再干一次活才再提醒一次（累计两条，不是每步一条）。
func TestLoop_TodoRefreshNotRepeatedAfterUpdate(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	registry := tools.NewRegistry()
	if err := registry.Register(NewTodoTool()); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&scriptTool{name: "read", result: tools.ToolResult{Content: "ok"}}); err != nil {
		t.Fatal(err)
	}
	item := func(status string) llm.StreamChunk {
		return llm.StreamChunk{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "t1", Name: "todo",
			ArgumentsDelta: `{"items":[{"text":"a","status":"` + status + `"}]}`}}}
	}
	work := llm.StreamChunk{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "read", ArgumentsDelta: `{}`}}}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{item("pending"), {EndReason: llm.EndDone}},
		{work, {EndReason: llm.EndDone}},         // 干活 → 该催（进入第 3 步的请求）
		{item("done"), {EndReason: llm.EndDone}}, // 重交 → 归零（第 4 步请求不该多一条）
		{work, {EndReason: llm.EndDone}},         // 又干活 → 再催一次（第 5 步请求多一条）
		{{Delta: "完成"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)

	ch, err := loop.Run(context.Background(), ledger, "干活")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)

	if len(fr.reqs) != 5 {
		t.Fatalf("请求数 = %d, want 5", len(fr.reqs))
	}
	count := func(i int) int { return strings.Count(messagesText(fr.reqs[i].Messages), "本轮提醒") }
	if got := count(2); got != 1 {
		t.Fatalf("干过活后应有一条提醒，实际 %d", got)
	}
	if got := count(3); got != 1 {
		t.Fatalf("重交后不该再多一条（同一张快照不反复催），实际 %d", got)
	}
	if got := count(4); got != 2 {
		t.Fatalf("重交后又干过活 → 再提醒一次，实际 %d", got)
	}
}
