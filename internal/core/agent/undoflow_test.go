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

// 0.0.07：undo 快照链路——工具结果里的 Undo 必须落账本（重启后仍可恢复），
// 实时事件携带撤销元数据（HasUndo/UndoPath），且旧全文**不**出现在实时事件
// 与模型可见消息里（只在账本，恢复走后端）。
func TestAgent_UndoFlowsToLedgerAndEvent(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	st := &scriptTool{name: "fs", result: tools.ToolResult{
		Content: "written a.txt (5 bytes)", Title: "a.txt", Op: "write",
		Undo: &tools.UndoData{
			Path: "a.txt", OldExists: true,
			OldContent: "THE-OLD-FULL-CONTENT", NewSHA256: "deadbeef",
		},
	}}
	registry := tools.NewRegistry()
	if err := registry.Register(st); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: "c1", Name: "fs", ArgumentsDelta: `{"action":"write"}`}}},
			{EndReason: llm.EndDone},
		},
		{{Delta: "done"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "test-model", registry)

	ch, err := loop.Run(context.Background(), ledger, "write it")
	if err != nil {
		t.Fatal(err)
	}
	chunks := drain(t, ch, 3*time.Second)

	// 实时事件：携带撤销元数据；旧全文不出现
	sawUndoMeta := false
	for _, c := range chunks {
		if c.ToolEvent == nil {
			continue
		}
		if strings.Contains(c.ToolEvent.Content, "THE-OLD-FULL-CONTENT") {
			t.Fatal("旧全文泄漏进实时事件")
		}
		if c.ToolEvent.HasUndo && c.ToolEvent.UndoPath == "a.txt" {
			sawUndoMeta = true
		}
	}
	if !sawUndoMeta {
		t.Fatal("终态事件必须携带撤销元数据（HasUndo/UndoPath）")
	}

	// 账本：undo 快照完整落账（重启后恢复的数据源）
	l2, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	var got *struct {
		OldContent string `json:"old_content"`
		NewSHA256  string `json:"new_sha256"`
	}
	if err := l2.Replay(func(ev session.Event) error {
		if ev.Kind() != session.EventToolResult {
			return nil
		}
		var p struct {
			Undo *struct {
				OldContent string `json:"old_content"`
				NewSHA256  string `json:"new_sha256"`
			} `json:"undo"`
		}
		if err := json.Unmarshal(ev.Data(), &p); err != nil {
			return err
		}
		if p.Undo != nil {
			got = &struct {
				OldContent string `json:"old_content"`
				NewSHA256  string `json:"new_sha256"`
			}{OldContent: p.Undo.OldContent, NewSHA256: p.Undo.NewSHA256}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.OldContent != "THE-OLD-FULL-CONTENT" || got.NewSHA256 != "deadbeef" {
		t.Fatalf("账本 undo 快照缺失或不完整：%+v", got)
	}

	// 模型可见消息：role=tool 的 Content 只有回执，不含旧全文
	for _, m := range fr.reqs[1].Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "THE-OLD-FULL-CONTENT") {
			t.Fatal("旧全文泄漏进模型上下文")
		}
	}
}
