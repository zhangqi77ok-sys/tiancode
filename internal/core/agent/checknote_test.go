package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/tools"
)

// 第 8 批：只存在于本次请求的补充说明（工作区检查结果）——进请求、不进账本、
// 更不改系统提示（系统提示逐字相同是 prompt cache 命中的前提）。
func TestLoop_TrailingNoteRequestOnly(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	fr := &fakeRuntime{script: [][]llm.StreamChunk{{{Delta: "好"}, {EndReason: llm.EndDone}}}}
	loop := NewLoop(fr, "m", tools.NewRegistry())
	loop.SetPreface("系统说明")
	loop.SetTrailingNote(func() string { return "【工作区检查，不是用户原话】foo.go:10: undefined" })

	ch, err := loop.Run(context.Background(), ledger, "帮我看下")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)

	msgs := fr.reqs[0].Messages
	if len(msgs) < 3 {
		t.Fatalf("msgs = %+v", msgs)
	}
	if msgs[0].Role != "system" || msgs[0].Content != "系统说明" {
		t.Fatalf("系统提示必须逐字不变：%+v", msgs[0])
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "不是用户原话") {
		t.Fatalf("附注应附在用户消息之后：%+v", last)
	}
	if ledgerContains(t, dir, "不是用户原话") {
		t.Fatal("附注只存在于本次请求，不得写进账本")
	}
}

// 返回空串 = 什么都不附（没有检查结果时不制造噪声）。
func TestLoop_TrailingNoteEmptyIsSilent(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	fr := &fakeRuntime{script: [][]llm.StreamChunk{{{Delta: "ok"}, {EndReason: llm.EndDone}}}}
	loop := NewLoop(fr, "m", tools.NewRegistry())
	loop.SetTrailingNote(func() string { return "  " })

	ch, err := loop.Run(context.Background(), ledger, "问")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 5*time.Second)
	if n := len(fr.reqs[0].Messages); n != 1 {
		t.Fatalf("空附注不该多出消息：msgs = %d", n)
	}
}
