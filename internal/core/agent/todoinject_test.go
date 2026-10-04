package agent

import (
	"strings"
	"testing"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
)

// appendEvent 是本文件的落账助手。
func appendEvent(t *testing.T, l *session.Ledger, kind session.EventKind, data any) session.Event {
	t.Helper()
	ev, err := l.Append(kind, data)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// C-AGT-5：账本有 EventTodo 时，派生消息末尾含**最新**一条清单全文，且只有一条。
func TestDerive_InjectsLatestTodoAtEnd(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()

	appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "干活"})
	appendEvent(t, l, session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "旧计划甲", Status: "pending"},
	}})
	appendEvent(t, l, session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "新计划乙", Status: "in_progress"},
	}})

	msgs, info, err := deriveMessagesWith(l, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "新计划乙") {
		t.Fatalf("末尾必须是最新清单：%q", last.Content)
	}
	if strings.Contains(last.Content, "旧计划甲") {
		t.Fatalf("只取最新快照，不得带出旧版本：%q", last.Content)
	}
	if n := strings.Count(last.Content, "任务清单·当前"); n != 1 {
		t.Fatalf("清单只注入一次，实际 %d 次", n)
	}
	// 读数必须顺带携带最新条目（自主续跑的结构化消费源，避免再扫账本）
	if len(info.LatestTodo) != 1 || info.LatestTodo[0].Text != "新计划乙" {
		t.Fatalf("DeriveInfo 必须携带最新条目：%+v", info.LatestTodo)
	}
}

// 全部 done 的清单**仍然注入**（写明"可收尾"）——模型需要知道计划已清空才会收尾。
func TestDerive_AllDoneTodoStillInjected(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()

	appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "干活"})
	appendEvent(t, l, session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "甲", Status: "done"},
		{Text: "乙", Status: "done"},
	}})

	msgs, _, err := deriveMessagesWith(l, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, "全部条目已标记完成") {
		t.Fatalf("全 done 清单必须注入且写明可收尾：%q", last.Content)
	}
}

// C-AGT-6：极小预算下清单**不被折叠**（五级折叠只作用于 tool/image/body 三个
// 索引表；此测试锁住"当前恰好成立"，防止将来被静默折掉）。
func TestDerive_TodoSurvivesBudgetFolding(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()

	for _, txt := range []string{"第一轮", "第二轮", "第三轮"} {
		appendEvent(t, l, session.EventUserMessage, map[string]any{"text": txt})
		appendEvent(t, l, session.EventAssistantMsg, map[string]string{"text": "回复 " + txt})
	}
	appendEvent(t, l, session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "必须全文保留的这一项", Status: "in_progress"},
	}})

	msgs, _, err := deriveMessagesWith(l, DeriveOptions{BudgetTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if !strings.Contains(m.Content, "必须全文保留的这一项") {
			continue
		}
		found = true
		if strings.Contains(m.Content, "已折叠") || strings.Contains(m.Content, "已省略") {
			t.Fatalf("清单被折叠了：%q", m.Content)
		}
	}
	if !found {
		t.Fatalf("极小预算下清单被整段丢弃：%+v", msgs)
	}
}

// C-AGT-7：没有 EventTodo 的账本，派生结果与旧版逐条一致（零噪声）。
func TestDerive_NoTodoNoInjection(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()

	appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "hi"})

	msgs, info, err := deriveMessagesWith(l, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" || msgs[0].Content != "hi" {
		t.Fatalf("无清单时不得凭空多出消息：%+v", msgs)
	}
	if info.LatestTodo != nil {
		t.Fatalf("无清单时读数必须为 nil：%+v", info.LatestTodo)
	}
}

// C-AGT-8：清单消息绝不能携带 tool_calls。
// 场景：assistant(text) → todo → tool_call → tool_result。若注入时误设
// lastAssistant，末尾 flush 会把待配对的 tool_calls 并进清单消息。
func TestDerive_TodoMessageNeverCarriesToolCalls(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()

	appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "改一下"})
	appendEvent(t, l, session.EventAssistantMsg, map[string]string{"text": "我先看看"})
	appendEvent(t, l, session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "改 A 这个文件", Status: "in_progress"},
	}})
	appendEvent(t, l, session.EventToolCall, map[string]any{
		"id": "t1", "name": "fs", "arguments": `{"action":"read","path":"a.go"}`,
	})
	appendEvent(t, l, session.EventToolResult, map[string]any{
		"id": "t1", "name": "fs", "content": "file body", "is_error": false, "title": "a.go",
	})
	appendEvent(t, l, session.EventAssistantMsg, map[string]string{"text": "读完了"})

	msgs, _, err := deriveMessagesWith(l, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sawTodo := false
	for _, m := range msgs {
		if !strings.Contains(m.Content, "改 A 这个文件") {
			continue
		}
		sawTodo = true
		if len(m.ToolCalls) > 0 {
			t.Fatalf("清单消息吸走了 tool_calls：%+v", m)
		}
	}
	if !sawTodo {
		t.Fatalf("清单消息缺失：%+v", msgs)
	}
}

// C-AGT-9：落在 fork 丢弃区间内的清单不得出现。from_seq 用 Append 返回的真实 seq。
func TestDerive_TodoInsideForkDropIsDiscarded(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()

	firstSeq := appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "第一轮"}).Seq()
	appendEvent(t, l, session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "被回退掉的旧计划", Status: "pending"},
	}})
	appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "第二轮"})
	appendEvent(t, l, session.EventAssistantMsg, map[string]string{"text": "干完了"})
	appendEvent(t, l, session.EventFork, map[string]any{"from_seq": firstSeq})
	appendEvent(t, l, session.EventUserMessage, map[string]any{"text": "重来"})

	msgs, info, err := deriveMessagesWith(l, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if strings.Contains(m.Content, "被回退掉的旧计划") {
			t.Fatalf("fork 丢弃区间内的清单复活了：%+v", msgs)
		}
	}
	if info.LatestTodo != nil {
		t.Fatalf("丢弃区间内取到的清单不得进读数：%+v", info.LatestTodo)
	}
}
