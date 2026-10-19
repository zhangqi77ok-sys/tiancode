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

// P1：输出被长度上限截断、最后一笔调用参数残缺 → 绝不执行；留痕并告诉模型拆小后继续。
func TestAgent_TruncatedToolCallNotExecuted(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	st := &scriptTool{name: "fs", result: tools.ToolResult{Content: "written"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{Delta: "开始写文件"},
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{"action":"write","path":"a.txt","content":"很长很长`}}},
			{EndReason: llm.EndDone, Truncated: true},
		},
		{{Delta: "已拆小完成"}, {EndReason: llm.EndDone}},
	}}
	ch, err := NewLoop(fr, "m", registry).Run(context.Background(), ledger, "写个大文件")
	if err != nil {
		t.Fatal(err)
	}
	var last llm.StreamChunk
	for _, c := range drain(t, ch, 5*time.Second) {
		if c.EndReason != llm.EndNone {
			last = c
		}
	}
	if last.EndReason != llm.EndDone {
		t.Fatalf("want EndDone, got %v err=%v", last.EndReason, last.Err)
	}
	if st.executed != 0 {
		t.Fatalf("参数残缺的调用绝不能执行：executed=%d", st.executed)
	}
	if len(fr.reqs) != 2 {
		t.Fatalf("应带着说明继续下一步：calls=%d", len(fr.reqs))
	}
	msgs := fr.reqs[1].Messages
	if lastMsg := msgs[len(msgs)-1]; lastMsg.Role != "user" || !strings.Contains(lastMsg.Content, "长度上限") {
		t.Fatalf("下一步请求应以截断说明结尾：%+v", lastMsg)
	}
	for _, m := range msgs {
		if len(m.ToolCalls) > 0 {
			t.Fatalf("被丢弃的调用不应进入上下文（会留下孤儿 tool_call）：%+v", m)
		}
	}
	notes := 0
	if err := ledger.Replay(func(ev session.Event) error {
		if ev.Kind() == session.EventToolCall {
			t.Fatalf("被丢弃的调用不应落账：%s", ev.Data())
		}
		if ev.Kind() == session.EventAssistantMsg && strings.Contains(string(ev.Data()), "长度上限") {
			notes++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if notes != 1 {
		t.Fatalf("账本应恰有一条截断留痕：%d", notes)
	}
}

// P1：前面完整的调用照常执行，只丢残缺的最后一笔。
func TestAgent_TruncatedKeepsCompleteCalls(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	st := &scriptTool{name: "fs", result: tools.ToolResult{Content: "ok"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{"action":"read","path":"a.txt"}`}}},
			{ToolCalls: []llm.ToolCallChunk{{Index: 1, ID: "c2", Name: "fs", ArgumentsDelta: `{"action":"write","path":"b`}}},
			{EndReason: llm.EndDone, Truncated: true},
		},
		{{Delta: "收尾"}, {EndReason: llm.EndDone}},
	}}
	ch, err := NewLoop(fr, "m", registry).Run(context.Background(), ledger, "读完再写")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)
	if st.executed != 1 {
		t.Fatalf("完整的 c1 应执行、残缺的 c2 不执行：executed=%d", st.executed)
	}
	msgs := fr.reqs[1].Messages
	if lastMsg := msgs[len(msgs)-1]; lastMsg.Role != "user" || !strings.Contains(lastMsg.Content, "长度上限") {
		t.Fatalf("工具结果之后应附截断说明：%+v", lastMsg)
	}
}

// P1：纯文本被截断 → 答复末尾如实注明（此前与正常写完无法区分）。
func TestAgent_TruncatedTextAnnotated(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "第一部分……"}, {EndReason: llm.EndDone, Truncated: true}},
	}}
	ch, err := NewLoop(fr, "m", nil).Run(context.Background(), ledger, "写长文")
	if err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	for _, c := range drain(t, ch, 5*time.Second) {
		streamed.WriteString(c.Delta)
	}
	if !strings.Contains(streamed.String(), "长度上限被截断") {
		t.Fatalf("界面流里应出现截断说明：%q", streamed.String())
	}
	anchored := ""
	if err := ledger.Replay(func(ev session.Event) error {
		if ev.Kind() == session.EventAssistantMsg {
			anchored = string(ev.Data())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(anchored, "长度上限被截断") {
		t.Fatalf("账本锚点应带截断说明：%s", anchored)
	}
}
