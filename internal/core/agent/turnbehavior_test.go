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

// 0.0.06：跨轮历史必须保留"工具调用前的助手正文"——下一轮 deriveMessages
// 把锚点正文并入 assistant(text, tool_calls)，模型才能看见自己"为什么调工具"；
// thinking 与半截 delta 绝不进入模型上下文。
func TestDerive_KeepsAssistantTextBeforeToolCalls(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "fs", result: tools.ToolResult{Content: "file-x"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{Thinking: "secret-chain-of-thought"},
			{Delta: "先看看文件"},
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "好了"}, {EndReason: llm.EndDone}},
		{{Delta: "第二轮回答"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)
	ch, err := loop.Run(context.Background(), ledger, "q1")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)
	// 第二轮：derive 从账本重放（不依赖本轮内存态）
	ch, err = loop.Run(context.Background(), ledger, "q2")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	msgs := fr.reqs[2].Messages
	// 期望形态：user(q1), assistant("先看看文件" + tool_calls), tool, assistant("好了"), user(q2)
	if len(msgs) != 5 {
		t.Fatalf("messages = %d, want 5: %+v", len(msgs), msgs)
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "先看看文件" || len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != "c1" {
		t.Fatalf("工具前的助手正文丢失或未并入 tool_calls 消息：msgs[1] = %+v", msgs[1])
	}
	if msgs[2].Role != "tool" || msgs[2].Content != "file-x" {
		t.Fatalf("msgs[2] = %+v", msgs[2])
	}
	if msgs[3].Role != "assistant" || msgs[3].Content != "好了" || len(msgs[3].ToolCalls) != 0 {
		t.Fatalf("msgs[3] = %+v", msgs[3])
	}
	// thinking 与半截 delta 不进上下文
	for i, m := range msgs {
		if strings.Contains(m.Content, "secret-chain") {
			t.Fatalf("thinking 泄漏进模型上下文（msgs[%d]）", i)
		}
	}
}

// 无正文的工具步（模型直接调工具）仍产出纯 assistant(tool_calls)，不并出空文本。
func TestDerive_ToolCallWithoutTextStaysPlain(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	st := &scriptTool{name: "fs", result: tools.ToolResult{Content: "ok"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "done"}, {EndReason: llm.EndDone}},
		{{Delta: "ok"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)
	ch, err := loop.Run(context.Background(), ledger, "q1")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)
	ch, err = loop.Run(context.Background(), ledger, "q2")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 3*time.Second)

	msgs := fr.reqs[2].Messages
	if len(msgs) != 5 {
		t.Fatalf("messages = %d, want 5: %+v", len(msgs), msgs)
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "" || len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("无正文工具步应保持纯 tool_calls 形态：msgs[1] = %+v", msgs[1])
	}
}

// 0.0.06：步数耗尽不再 EndError——强制一步空工具总结后正常收束（EndDone、
// turn_end 落账、总结步不带任何工具定义）。
func TestAgent_StepLimitForcesSummaryInsteadOfError(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "fs", result: tools.ToolResult{Content: "ok"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	script := make([][]llm.StreamChunk, 0, MaxStepsPerTurn+1)
	for i := 0; i < MaxStepsPerTurn; i++ {
		script = append(script, []llm.StreamChunk{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{}`}}},
			{EndReason: llm.EndDone},
		})
	}
	script = append(script, []llm.StreamChunk{{Delta: "总结：已完成 25 次查看，没有未完成项。"}, {EndReason: llm.EndDone}})
	fr := &fakeRuntime{script: script}
	loop := NewLoop(fr, "m", registry)
	ch, err := loop.Run(context.Background(), ledger, "q")
	if err != nil {
		t.Fatal(err)
	}
	chunks := drain(t, ch, 5*time.Second)

	// 终态：恰好一个 EndDone（绝不是 EndError）
	terminal := 0
	for _, c := range chunks {
		if c.EndReason != llm.EndNone {
			terminal++
			if c.EndReason != llm.EndDone {
				t.Fatalf("步数耗尽应正常收束，got EndReason=%v err=%v", c.EndReason, c.Err)
			}
		}
	}
	if terminal != 1 {
		t.Fatalf("terminal chunks = %d, want 1", terminal)
	}

	// 总结步请求：无工具定义，末尾带强制总结指令
	last := fr.reqs[len(fr.reqs)-1]
	if len(last.Tools) != 0 {
		t.Fatalf("总结步必须为空工具集，got %d tools", len(last.Tools))
	}
	lastMsg := last.Messages[len(last.Messages)-1]
	if lastMsg.Role != "user" || !strings.Contains(lastMsg.Content, "步数上限") {
		t.Fatalf("总结步缺强制指令：%+v", lastMsg)
	}

	// 账本：turn_end(done) 恰好一次；总结文本进了锚点
	if n := countEvents(t, dir, session.EventTurnEnd); n != 1 {
		t.Fatalf("turn_end events = %d, want 1", n)
	}
	found := false
	l, _ := session.OpenLedger(dir, "s1")
	defer l.Close()
	_ = l.Replay(func(ev session.Event) error {
		if ev.Kind() == session.EventAssistantMsg && strings.Contains(string(ev.Data()), "总结：已完成") {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("总结文本未落锚点")
	}
}
