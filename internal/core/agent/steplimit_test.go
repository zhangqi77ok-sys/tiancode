package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// 第 3 批：步数分段——一段用尽先询问（可取消），同意续跑，拒绝/取消正常收尾。

// fakeAsker 记录调用并按键序返回剧本答复。
type fakeAsker struct {
	replies []string
	calls   int
	last    AskRequest
}

func (f *fakeAsker) Ask(_ context.Context, req AskRequest) (string, error) {
	f.calls++
	f.last = req
	if len(f.replies) == 0 {
		return "", nil
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return r, nil
}

// stepScript 生成 n 个"工具调用步"（每步 1 次 noop 调用 + EndDone）。
func stepScript(n int) [][]llm.StreamChunk {
	out := make([][]llm.StreamChunk, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, []llm.StreamChunk{
			{ToolCalls: []llm.ToolCallChunk{{Index: 0, ID: fmt.Sprintf("t%d", i), Name: "noop", ArgumentsDelta: "{}"}}},
			{EndReason: llm.EndDone},
		})
	}
	return out
}

func assertTurnEnd(t *testing.T, ledger *session.Ledger) {
	t.Helper()
	found := false
	if err := ledger.Replay(func(ev session.Event) error {
		if ev.Kind() == session.EventTurnEnd {
			found = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("账本必须有 TurnEnd（正常收尾）")
	}
}

// 步数用尽必须**先询问**（不再直接打无工具总结）；同意后续跑一段并正常完成。
func TestLoop_StepLimitAskThenContinue(t *testing.T) {
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
	asker := &fakeAsker{replies: []string{"继续执行"}}
	loop.SetAsker(asker)

	ch, err := loop.Run(context.Background(), ledger, "hi")
	if err != nil {
		t.Fatal(err)
	}
	chunks := drain(t, ch, 10*time.Second)
	var terminal llm.StreamChunk
	for _, c := range chunks {
		if c.EndReason != llm.EndNone {
			terminal = c
		}
	}
	if asker.calls != 1 {
		t.Fatalf("步数用尽必须先询问用户：asker.calls=%d", asker.calls)
	}
	if terminal.EndReason != llm.EndDone {
		t.Fatalf("同意续跑后应正常完成：%v（%v）", terminal.EndReason, terminal.Err)
	}
	assertTurnEnd(t, ledger)
}

// 用户拒绝续跑 → 跳出分段循环走收尾：模型侧总结调用仍发生（正常结束，非错误）。
func TestLoop_StepLimitDeclineEndsNormally(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	script := stepScript(MaxStepsPerTurn)
	script = append(script, []llm.StreamChunk{{Delta: "总结"}, {EndReason: llm.EndDone}})

	fr := &fakeRuntime{script: script}
	registry := tools.NewRegistry()
	if err := registry.Register(&noopTool{}); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(fr, "m", registry)
	asker := &fakeAsker{replies: []string{"就此结束"}}
	loop.SetAsker(asker)

	ch, err := loop.Run(context.Background(), ledger, "hi")
	if err != nil {
		t.Fatal(err)
	}
	chunks := drain(t, ch, 10*time.Second)
	var terminal llm.StreamChunk
	for _, c := range chunks {
		if c.EndReason != llm.EndNone {
			terminal = c
		}
	}
	if asker.calls != 1 {
		t.Fatalf("拒绝路径同样必须先询问：asker.calls=%d", asker.calls)
	}
	if terminal.EndReason != llm.EndDone {
		t.Fatalf("拒绝续跑应正常结束（EndDone）：%v（%v）", terminal.EndReason, terminal.Err)
	}
	assertTurnEnd(t, ledger)
	// 询问发生在收尾之前：第 26 次模型调用（索引 25）必须是"无工具总结"形态
	if len(fr.reqs) < MaxStepsPerTurn+1 {
		t.Fatalf("模型调用数不足：%d", len(fr.reqs))
	}
	if len(fr.reqs[MaxStepsPerTurn].Tools) != 0 {
		t.Fatalf("步数用尽后的收尾调用不得携带工具：%d", len(fr.reqs[MaxStepsPerTurn].Tools))
	}
}
