package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// 第 7 批：输入框里指定技能——本轮**第一笔**工具调用必须是 skill 且 name 正确，
// 且技能正文在模型开口前就进了上下文（不是靠用户文字里写"请使用某技能"）。
func TestLoop_ForcedSkillCalledFirst(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "skill", result: tools.ToolResult{Content: "技能正文", Title: "deploy", Op: "skill"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{{{Delta: "按技能做"}, {EndReason: llm.EndDone}}}}
	loop := NewLoop(fr, "m", registry)
	loop.SetForcedTool(&ForcedTool{Name: "skill", Arguments: `{"name":"deploy"}`})

	ch, err := loop.Run(context.Background(), ledger, "按这个技能来")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)

	calls := toolCallEvents(t, dir)
	if len(calls) != 1 || calls[0].Name != "skill" || !strings.Contains(calls[0].Arguments, `"deploy"`) {
		t.Fatalf("第一笔工具调用 = %+v, want skill/deploy", calls)
	}
	if st.executed != 1 {
		t.Fatalf("skill 执行次数 = %d, want 1", st.executed)
	}
	if fr.requestCount() != 1 {
		t.Fatalf("requests = %d, want 1（强制调用在模型开口前完成）", fr.requestCount())
	}
	if got := messagesText(fr.reqs[0].Messages); !strings.Contains(got, "技能正文") {
		t.Fatalf("技能正文必须在模型第一次请求里就可见：%s", got)
	}
}

// 第 8 批：选中 MCP 改为"钉住"——程序不发这笔调用，而是在本次请求里附一句约束；
// 模型第一笔调用必须是钉住的 server/tool，参数由模型那次调用提供（菜单不再代填 {}）；
// 约束只存在于本次请求，不落账本（系统提示也必须逐字不变）。
func TestLoop_PinnedMCPFirstCall(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "mcp", result: tools.ToolResult{Content: "issue created", Title: "github/create_issue", Op: "mcp"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "mcp",
				ArgumentsDelta: `{"server":"github","tool":"create_issue","arguments":{"title":"修登录"}}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "已创建"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", registry)
	loop.SetForcedTool(&ForcedTool{Name: "mcp", Server: "github", Tool: "create_issue"})

	ch, err := loop.Run(context.Background(), ledger, "在 github 建个 issue")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)

	calls := toolCallEvents(t, dir)
	if len(calls) != 1 || calls[0].Name != "mcp" {
		t.Fatalf("第一笔必须是 mcp：%+v", calls)
	}
	if !strings.Contains(calls[0].Arguments, `"server":"github"`) || !strings.Contains(calls[0].Arguments, `"tool":"create_issue"`) {
		t.Fatalf("server/tool 必须原样：%s", calls[0].Arguments)
	}
	if !strings.Contains(calls[0].Arguments, `"title":"修登录"`) {
		t.Fatalf("参数必须来自模型那次调用，而不是菜单代填的空对象：%s", calls[0].Arguments)
	}
	if st.executed != 1 {
		t.Fatalf("mcp 执行次数 = %d, want 1", st.executed)
	}
	// 约束在请求里（第一次请求就带），但绝不落账本
	got := messagesText(fr.reqs[0].Messages)
	if !strings.Contains(got, "本轮约束") || !strings.Contains(got, "github") || !strings.Contains(got, "create_issue") {
		t.Fatalf("请求里应带钉住约束：%s", got)
	}
	if ledgerContains(t, dir, "本轮约束") {
		t.Fatal("钉住约束只存在于本次请求，不得写进账本")
	}
}

// 模型改了 server/tool（或干脆不调用）：本轮停在这条错误上——被改过的调用不执行、
// 不落账、不再问模型（不继续空答）。
func TestLoop_PinnedMCPWrongCallStopsTurn(t *testing.T) {
	cases := []struct {
		name string
		step []llm.StreamChunk
	}{
		{"换成了别的 tool", []llm.StreamChunk{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "mcp",
				ArgumentsDelta: `{"server":"github","tool":"list","arguments":{}}`}}},
			{EndReason: llm.EndDone},
		}},
		{"直接回答不调用", []llm.StreamChunk{{Delta: "我直接说答案"}, {EndReason: llm.EndDone}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ledger, dir := newTestLedger(t)
			defer ledger.Close()

			st := &scriptTool{name: "mcp", result: tools.ToolResult{Content: "不该发生"}}
			registry := tools.NewRegistry()
			if err := registry.Register(st); err != nil {
				t.Fatal(err)
			}
			fr := &fakeRuntime{script: [][]llm.StreamChunk{c.step, {{Delta: "空答"}, {EndReason: llm.EndDone}}}}
			loop := NewLoop(fr, "m", registry)
			loop.SetForcedTool(&ForcedTool{Name: "mcp", Server: "github", Tool: "create_issue"})

			ch, err := loop.Run(context.Background(), ledger, "建个 issue")
			if err != nil {
				t.Fatal(err)
			}
			out := drain(t, ch, 5*time.Second)

			if fr.requestCount() != 1 {
				t.Fatalf("停轮后不得再问模型：requests=%d", fr.requestCount())
			}
			if st.executed != 0 || len(toolCallEvents(t, dir)) != 0 {
				t.Fatal("被改过的调用既不该执行也不该落账")
			}
			if n := countEvents(t, dir, session.EventAssistantMsg); n != 0 {
				t.Fatalf("assistant anchors = %d, want 0（不许空答落账）", n)
			}
			if n := countEvents(t, dir, session.EventError); n == 0 {
				t.Fatal("违规必须落一条 EventError（重放可见）")
			}
			var reason string
			for _, ch2 := range out {
				if ch2.EndReason == llm.EndError && ch2.Err != nil {
					reason = ch2.Err.Error()
				}
			}
			if !strings.Contains(reason, "指定的 MCP 工具没有被调用") {
				t.Fatalf("终态要写明原因：%q", reason)
			}
		})
	}
}

// "带参数的旧消息"（第 7 批界面遗留）仍走程序代发、参数原样传入。
func TestLoop_LegacyMCPWithArgsStillIssued(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "mcp", result: tools.ToolResult{Content: "ok", Op: "mcp"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{{{Delta: "完成"}, {EndReason: llm.EndDone}}}}
	loop := NewLoop(fr, "m", registry)
	loop.SetForcedTool(&ForcedTool{Name: "mcp", Server: "github", Tool: "create_issue", Arguments: `{"title":"旧消息"}`})

	ch, err := loop.Run(context.Background(), ledger, "建个 issue")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)

	calls := toolCallEvents(t, dir)
	if len(calls) != 1 || calls[0].Name != "mcp" {
		t.Fatalf("旧消息仍应由程序代发：%+v", calls)
	}
	if !strings.Contains(calls[0].Arguments, `"旧消息"`) || !strings.Contains(calls[0].Arguments, `"tool":"create_issue"`) {
		t.Fatalf("用户手写的参数必须原样传入：%s", calls[0].Arguments)
	}
}

// 强制调用失败（技能不存在 / MCP 没连上）：本轮停在这条工具错误上——不假装用过，
// 也不让模型空答（零模型请求）。
func TestLoop_ForcedToolFailureStopsTurn(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "skill", result: tools.ToolResult{
		Content: "没有名为 deploy 的已启用技能", IsError: true, Title: "deploy", Op: "skill",
	}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{{{Delta: "不该发生"}, {EndReason: llm.EndDone}}}}
	loop := NewLoop(fr, "m", registry)
	loop.SetForcedTool(&ForcedTool{Name: "skill", Arguments: `{"name":"deploy"}`})

	ch, err := loop.Run(context.Background(), ledger, "用 deploy 技能")
	if err != nil {
		t.Fatal(err)
	}
	out := drain(t, ch, 5*time.Second)

	if fr.requestCount() != 0 {
		t.Fatalf("强制调用失败后不得再请求模型：requests=%d", fr.requestCount())
	}
	var terminal *llm.StreamChunk
	errCard := false
	for i := range out {
		if out[i].EndReason != llm.EndNone {
			terminal = &out[i]
		}
		if out[i].ToolEvent != nil && out[i].ToolEvent.Status == "error" {
			errCard = true
		}
	}
	if !errCard {
		t.Fatal("失败的工具卡必须上抛（用户看得见这条工具没成功）")
	}
	if terminal == nil || terminal.EndReason != llm.EndError {
		t.Fatalf("终态 = %+v, want EndError", terminal)
	}
	if terminal.Err == nil || !strings.Contains(terminal.Err.Error(), "不可用") {
		t.Fatalf("终态要写明原因：%+v", terminal.Err)
	}
	if n := countEvents(t, dir, session.EventAssistantMsg); n != 0 {
		t.Fatalf("assistant anchors = %d, want 0（本轮没有模型回复）", n)
	}
}

// 没指定强制工具时行为与旧版一致：无凭空工具调用。
func TestLoop_NoForcedToolUnchanged(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "skill", result: tools.ToolResult{Content: "x"}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{{{Delta: "直接回答"}, {EndReason: llm.EndDone}}}}
	loop := NewLoop(fr, "m", registry)

	ch, err := loop.Run(context.Background(), ledger, "你好")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)

	if len(toolCallEvents(t, dir)) != 0 {
		t.Fatal("未指定强制工具时不得凭空产生工具调用")
	}
	if st.executed != 0 {
		t.Fatal("未指定强制工具时不得执行任何工具")
	}
	if fr.requestCount() != 1 {
		t.Fatalf("requests = %d, want 1", fr.requestCount())
	}
}

// 白名单：强制调用不得成为"绕过程序直接跑 shell/fs"的口子。
func TestLoop_ForcedToolWhitelist(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	sh := &scriptTool{name: "shell", result: tools.ToolResult{Content: "should not run"}}
	registry := tools.NewRegistry()
	if err := registry.Register(sh); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{{{Delta: "不该发生"}, {EndReason: llm.EndDone}}}}
	loop := NewLoop(fr, "m", registry)
	loop.SetForcedTool(&ForcedTool{Name: "shell", Arguments: `{"command":"rm -rf /"}`})

	ch, err := loop.Run(context.Background(), ledger, "跑个命令")
	if err != nil {
		t.Fatal(err)
	}
	out := drain(t, ch, 5*time.Second)

	if sh.executed != 0 || len(toolCallEvents(t, dir)) != 0 {
		t.Fatal("非白名单工具绝不能被强制调用")
	}
	if fr.requestCount() != 0 {
		t.Fatalf("被拒绝的强制调用不得继续问模型：requests=%d", fr.requestCount())
	}
	var reason string
	for _, c := range out {
		if c.EndReason == llm.EndError && c.Err != nil {
			reason = c.Err.Error()
		}
	}
	if !strings.Contains(reason, "不支持的强制工具") {
		t.Fatalf("终态要写明拒绝原因：%q", reason)
	}
}

// toolCallEvents 按顺序读出账本里的工具调用（name + arguments）。
type toolCallRec struct {
	Name      string
	Arguments string
}

func toolCallEvents(t *testing.T, dir string) []toolCallRec {
	t.Helper()
	l, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var out []toolCallRec
	if err := l.Replay(func(ev session.Event) error {
		if ev.Kind() != session.EventToolCall {
			return nil
		}
		var p struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err != nil {
			return err
		}
		out = append(out, toolCallRec{Name: p.Name, Arguments: p.Arguments})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// ledgerContains 报告账本里是否出现过某段文字（"仅本次请求"的说明不得落账）。
func ledgerContains(t *testing.T, dir, needle string) bool {
	t.Helper()
	l, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	found := false
	if err := l.Replay(func(ev session.Event) error {
		if strings.Contains(string(ev.Data()), needle) {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return found
}

// messagesText 把一次请求的消息拼成文本（断言"模型看到了什么"）。
func messagesText(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Role)
		b.WriteString(":")
		b.WriteString(m.Content)
		b.WriteString(" | ")
	}
	return b.String()
}
