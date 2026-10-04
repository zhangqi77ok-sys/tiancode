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

// C-AGT-12（配置侧）：默认额度为 0；setter 生效；负数归零。
func TestLoop_AutoContinueBudgetDefaultsToZero(t *testing.T) {
	loop := NewLoop(nil, "m", nil)
	if loop.autoBudget != 0 {
		t.Fatalf("未开启时额度必须为 0：%d", loop.autoBudget)
	}
	loop.SetAutoContinueSegments(3)
	if loop.autoBudget != 3 {
		t.Fatalf("额度未生效：%d", loop.autoBudget)
	}
	loop.SetAutoContinueSegments(-5)
	if loop.autoBudget != 0 {
		t.Fatalf("负额度必须归零：%d", loop.autoBudget)
	}
}

// 额度耗尽判定：额度是唯一刹车，触顶后 autoQuotaLeft 必须为 0。
func TestLoop_AutoQuotaExhausts(t *testing.T) {
	loop := NewLoop(nil, "m", nil)
	loop.SetAutoContinueSegments(2)
	if got := loop.autoQuotaLeft(); got != 2 {
		t.Fatalf("初始额度 %d，want 2", got)
	}
	loop.autoSegments = 2
	if got := loop.autoQuotaLeft(); got != 0 {
		t.Fatalf("用尽后应为 0，实际 %d", got)
	}
}

// C-AGT-14：解析只接受三种合法值；JSON 坏、字段缺失、取值非法一律降级 blocked。
// fail-closed——解析不了就问用户，绝不猜一个"继续"出去。
func TestParseAutoVerdict(t *testing.T) {
	cases := []struct {
		in   string
		want autoVerdict
	}{
		{`{"verdict":"done","reason":"都改完了"}`, verdictDone},
		{`前面有闲聊 {"verdict":"continue","reason":"还有三处"} 后面有话`, verdictContinue},
		{`{"verdict":"blocked","reason":"不知道从哪下手"}`, verdictBlocked},
		{`{"verdict":"whatever"}`, verdictBlocked},
		{`{"reason":"缺 verdict 字段"}`, verdictBlocked},
		{`完全不是 JSON`, verdictBlocked},
		{``, verdictBlocked},
	}
	for _, c := range cases {
		if got := parseAutoVerdict(c.in); got != c.want {
			t.Fatalf("parseAutoVerdict(%q) = %q，want %q", c.in, got, c.want)
		}
	}
}

// C-AGT-10：自评调用不得携带工具定义——否则模型会"边自评边继续干活"，
// 自评失去判断意义且步数失控。
func TestLoop_SelfAssessCarriesNoTools(t *testing.T) {
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: `{"verdict":"continue","reason":"还有三处要改"}`}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", nil)
	loop.SetAutoContinueSegments(2)

	verdict, reason, err := loop.selfAssess(context.Background(),
		[]llm.Message{{Role: "user", Content: "干活"}},
		[]llm.TodoItem{{Text: "甲", Status: "pending"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if verdict != verdictContinue || !strings.Contains(reason, "三处") {
		t.Fatalf("裁决解析错：%q / %q", verdict, reason)
	}
	if len(fr.reqs) != 1 {
		t.Fatalf("应只发一次请求，实际 %d", len(fr.reqs))
	}
	if len(fr.reqs[0].Tools) != 0 {
		t.Fatalf("自评调用不得携带工具：%d 个", len(fr.reqs[0].Tools))
	}
}

// C-AGT-13：清单全部 done 时不得采纳 continue（防自欺，结构化判定而非文本匹配）。
func TestLoop_SelfAssessRefusesContinueWhenAllDone(t *testing.T) {
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: `{"verdict":"continue","reason":"我还能干"}`}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", nil)
	loop.SetAutoContinueSegments(5)

	verdict, _, err := loop.selfAssess(context.Background(),
		[]llm.Message{{Role: "user", Content: "继续"}},
		[]llm.TodoItem{{Text: "甲", Status: "done"}, {Text: "乙", Status: "done"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if verdict != verdictDone {
		t.Fatalf("清单全 done 时不得采纳 continue，实际 %q", verdict)
	}
}

// 从未提交过清单（items 为空）时自评不得判 done——没有清单就谈不上"清单完成"，
// 必须降级 blocked 去问用户（否则没有清单的轮次会被无声放行）。
func TestLoop_SelfAssessWithoutTodoIsBlocked(t *testing.T) {
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: `{"verdict":"done","reason":"我觉得完成了"}`}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "m", nil)
	loop.SetAutoContinueSegments(2)

	verdict, _, err := loop.selfAssess(context.Background(),
		[]llm.Message{{Role: "user", Content: "干活"}}, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if verdict != verdictBlocked {
		t.Fatalf("无清单时自评必须降级 blocked，实际 %q", verdict)
	}
}

// C-AGT-12（行为侧）：额度 > 0 且模型自评 done → 不问用户直接收尾。
func TestLoop_AutoAssessDoneEndsWithoutAsking(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	// 必须有"仍有未完成项"的清单：无清单时 selfAssess 按 C-AGT-13 强制 blocked，
	// 永远到不了模型的 done 裁决（这正是防自欺的行为）。
	if _, err := ledger.Append(session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "还有一半没做", Status: "in_progress"},
	}}); err != nil {
		t.Fatal(err)
	}

	// 时序：段 1 的 25 步（索引 0-24）→ 自评（索引 25，答 done）→ allDone 跳出
	// → "无工具总结"收尾（索引 26）。
	script := stepScript(MaxStepsPerTurn)
	script = append(script, []llm.StreamChunk{
		{Delta: `{"verdict":"done","reason":"全部完成"}`}, {EndReason: llm.EndDone},
	})
	script = append(script, []llm.StreamChunk{{Delta: "收尾"}, {EndReason: llm.EndDone}})

	fr := &fakeRuntime{script: script}
	registry := tools.NewRegistry()
	if err := registry.Register(&noopTool{}); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(fr, "m", registry)
	loop.SetAutoContinueSegments(2)
	asker := &fakeAsker{}
	loop.SetAsker(asker)

	ch, err := loop.Run(context.Background(), ledger, "干一个大活")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 10*time.Second)
	if asker.calls != 0 {
		t.Fatalf("自评 done 时不得打扰用户：asker.calls=%d", asker.calls)
	}
}

// C-AGT-11：触顶后必须回落到问用户（额度是唯一刹车）。
// 时序：段 1 的 25 步（索引 0-24）→ 自评（索引 25，答 continue）→ 段 2 的 25 步
// （索引 26-50）→ 额度已尽走询问（不占脚本）→ 用户同意 → 段 3 第 1 步即"收尾"
// （索引 51，无工具 → 最终回答）。脚本必须与该调用序严格对齐，错位会让自评弹到 noop 响应。
func TestLoop_AutoQuotaThenAskUser(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	// 同样必须落清单：无清单时 selfAssess 按 C-AGT-13 强制 blocked，第一段就会问用户。
	if _, err := ledger.Append(session.EventTodo, map[string]any{"items": []llm.TodoItem{
		{Text: "活还没干完", Status: "in_progress"},
	}}); err != nil {
		t.Fatal(err)
	}

	script := stepScript(MaxStepsPerTurn)
	script = append(script, []llm.StreamChunk{
		{Delta: `{"verdict":"continue","reason":"还有活"}`}, {EndReason: llm.EndDone},
	})
	script = append(script, stepScript(MaxStepsPerTurn)...)
	script = append(script, []llm.StreamChunk{{Delta: "收尾"}, {EndReason: llm.EndDone}})

	fr := &fakeRuntime{script: script}
	registry := tools.NewRegistry()
	if err := registry.Register(&noopTool{}); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(fr, "m", registry)
	loop.SetAutoContinueSegments(1)
	asker := &fakeAsker{replies: []string{"继续执行"}}
	loop.SetAsker(asker)

	ch, err := loop.Run(context.Background(), ledger, "干一个更大的活")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, ch, 30*time.Second)
	if asker.calls != 1 {
		t.Fatalf("额度触顶后必须问用户：asker.calls=%d", asker.calls)
	}
}
