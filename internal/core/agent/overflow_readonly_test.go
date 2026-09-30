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

// 0.0.11：MCP 工具的只读判定——只有 tool=list 是纯查询，其余一律串行
// （server 侧工具名对我们是黑盒，可能是写操作；白名单宁可窄）。
func TestIsReadOnlyCall_MCP(t *testing.T) {
	cases := []struct {
		args string
		want bool
	}{
		{`{"server":"fs","tool":"list"}`, true},
		{`{"server":"fs","tool":" List "}`, true}, // 空白与大小写不敏感
		{`{"server":"fs","tool":"read_file"}`, false},
		{`{"server":"fs","tool":"write_file"}`, false},
		{`{}`, false},
		{`not-json`, false},
	}
	for _, c := range cases {
		if got := isReadOnlyCall(llm.ToolCall{Name: "mcp", Arguments: c.args}); got != c.want {
			t.Fatalf("isReadOnlyCall(mcp, %s) = %v, want %v", c.args, got, c.want)
		}
	}
	// 既有白名单不受影响：shell 必须串行（两个人同时动同一资源代价更大）
	if isReadOnlyCall(llm.ToolCall{Name: "shell", Arguments: `{"command":"go build"}`}) {
		t.Fatal("shell 必须串行")
	}
}

// 0.0.11：折叠三级后仍超预算 → 不发请求，直接给明确错误（用户消息已落账本）。
// 为什么重要：发出去必然被上游按上下文长度拒绝，还会把"本地预算不够"伪装成上游错误。
func TestLoop_ContextOverflowFailsBeforeRequest(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{
		"text": strings.Repeat("这一段历史很长，用来把预算撑爆。", 20),
	}); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{{{Delta: "不该发生"}, {EndReason: llm.EndDone}}}}
	loop := NewLoop(fr, "m", nil)
	loop.SetContextBudget(1, false) // 极小预算：折完仍装不下

	_, err := loop.Run(context.Background(), ledger, "再来一轮")
	if err == nil {
		t.Fatal("折完仍超预算必须直接报错，不发请求")
	}
	if !strings.Contains(err.Error(), "超过渠道上限") {
		t.Fatalf("错误必须说清成因与出路：%v", err)
	}
	if len(fr.reqs) != 0 {
		t.Fatalf("不得向上游发请求：calls=%d", len(fr.reqs))
	}
	hasErr := false
	if rerr := ledger.Replay(func(ev session.Event) error {
		if ev.Kind() == session.EventError {
			hasErr = true
		}
		return nil
	}); rerr != nil {
		t.Fatal(rerr)
	}
	if !hasErr {
		t.Fatal("超预算失败必须落 error 事件（重放可见这轮为何失败）")
	}
	if loop.Phase() != PhaseIdle {
		t.Fatalf("失败后必须回到 Idle（否则该会话永久忙）：%v", loop.Phase())
	}
}

// 0.0.11：续跑前重新折叠——第二段开始时会重新派生并再报一次油表读数
// （此前续跑后仍显示开局数字，用户以为折叠没生效）。
func TestLoop_ContinueRefoldsContext(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	script := stepScript(MaxStepsPerTurn)
	script = append(script, []llm.StreamChunk{{Delta: "收尾"}, {EndReason: llm.EndDone}})
	fr := &fakeRuntime{script: script}
	registry := tools.NewRegistry()
	if err := registry.Register(&noopTool{}); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(fr, "m", registry)
	loop.SetContextBudget(100000, false) // 配了预算 → 每段上报油表读数（含续跑前的重新折叠）
	loop.SetAsker(&fakeAsker{replies: []string{"继续执行"}})

	ch, err := loop.Run(context.Background(), ledger, "hi")
	if err != nil {
		t.Fatal(err)
	}
	contexts := 0
	for _, c := range drain(t, ch, 10*time.Second) {
		if c.Context != nil {
			contexts++
		}
	}
	if contexts < 2 {
		t.Fatalf("续跑前必须重新折叠并刷新油表读数（期望 ≥2 次上报）：实际 %d", contexts)
	}
}
