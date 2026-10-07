package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
)

// C-AGT-9：压缩事件生效——截点前的叙事事件不投影、摘要以【历史摘要】消息开头、
// 截点后的轮次完整保留；最新任务清单不受截点影响（状态事件照常扫描）。
func TestDerive_CompactionReplacesOldTurns(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	appendTurn := func(id, user, answer string) {
		if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": user}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": answer}); err != nil {
			t.Fatal(err)
		}
	}
	appendTurn("t1", "第一问：项目目标是什么", "目标做压缩功能")
	if _, err := ledger.Append(session.EventTodo, map[string]any{
		"items": []llm.TodoItem{{Text: "实现压缩", Status: "in_progress"}},
	}); err != nil {
		t.Fatal(err)
	}
	// 记下第二问用户消息的 seq 作为截点：第一轮 + 清单被归档
	turn2Seq := ledger.NextSeq()
	appendTurn("t2", "第二问：选哪个方案", "选了账本截点方案")
	appendTurn("t3", "第三问：写测试了吗", "写了")
	appendTurn("t4", "第四问：收尾吧", "收尾中")
	if _, err := ledger.Append(session.EventCompaction, map[string]any{
		"up_to_seq": turn2Seq, "summary": "项目目标：做历史压缩；已决定用账本截点方案。",
	}); err != nil {
		t.Fatal(err)
	}

	msgs, info, err := deriveMessagesWith(ledger, DeriveOptions{BudgetTokens: 8000})
	if err != nil {
		t.Fatal(err)
	}
	if info.CompactionUpTo != turn2Seq || !strings.Contains(info.CompactionSummary, "账本截点方案") {
		t.Fatalf("DeriveInfo 未携带压缩读数：%+v", info)
	}
	// 首条是摘要消息
	if msgs[0].Role != "user" || !strings.HasPrefix(msgs[0].Content, compactionMarker) ||
		!strings.Contains(msgs[0].Content, "账本截点方案") {
		t.Fatalf("首条应为压缩摘要消息：%+v", msgs[0])
	}
	joined := compactionMessagesText(msgs)
	// 截点前的用户轮不再出现（第一问）；截点后的轮次完整保留
	if strings.Contains(joined, "第一问") {
		t.Fatal("截点前的用户轮不应再投影")
	}
	for _, want := range []string{"第二问", "第三问", "第四问", "选了账本截点方案"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("截点后内容丢失：%s", want)
		}
	}
	// 清单照常注入（事件在截点之前，但它是状态不是叙事）
	foundTodo := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "实现压缩") {
			foundTodo = true
		}
	}
	if !foundTodo {
		t.Fatal("截点前的任务清单应照常注入")
	}
}

// C-AGT-9：多次压缩，后一次覆盖前一次（最新截点与摘要生效）。
func TestDerive_CompactionLatestWins(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	cn := []string{"一", "二", "三", "四", "五", "六", "七"}
	for i := 1; i <= 4; i++ {
		if _, err := ledger.Append(session.EventUserMessage, map[string]string{
			"text": "第" + cn[i-1] + "问",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{
			"text": "答" + cn[i-1],
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ledger.Append(session.EventCompaction, map[string]any{
		"up_to_seq": 3, "summary": "第一次摘要",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventCompaction, map[string]any{
		"up_to_seq": 5, "summary": "第二次摘要",
	}); err != nil {
		t.Fatal(err)
	}
	msgs, info, err := deriveMessagesWith(ledger, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if info.CompactionUpTo != 5 || !strings.Contains(info.CompactionSummary, "第二次") {
		t.Fatalf("应以最新一次压缩为准：%+v", info)
	}
	if !strings.Contains(msgs[0].Content, "第二次摘要") || strings.Contains(msgs[0].Content, "第一次摘要") {
		t.Fatalf("摘要消息应为最新一次：%q", msgs[0].Content)
	}
	if strings.Contains(compactionMessagesText(msgs), "第一问") {
		t.Fatal("最新截点前的轮次不应投影")
	}
	if !strings.Contains(compactionMessagesText(msgs), "第三问") {
		t.Fatal("最新截点后的轮次应保留")
	}
}

// C-AGT-10：折叠到底仍超预算（声明上限）→ 自动压缩 → 带摘要继续，不再硬报错。
// 摘要调用走同一个 runtime（脚本第 1 段），正式对话是第 2 段。
func TestAgent_OverflowTriggersCompaction(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	big := strings.Repeat("背景讨论。", 20) // 每轮 100 字；6 轮的用户原话（永不折叠）足以超预算
	cn := []string{"一", "二", "三", "四", "五", "六", "七"}
	for i := 0; i < 6; i++ {
		if _, err := ledger.Append(session.EventUserMessage, map[string]string{
			"text": big + "（第" + cn[i] + "轮）",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{
			"text": "已收到第" + cn[i] + "轮",
		}); err != nil {
			t.Fatal(err)
		}
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{Delta: "用户在推进一个多轮任务，前两轮讨论了背景。"}, {EndReason: llm.EndDone}},
		{{Delta: "继续干活。"}, {EndReason: llm.EndDone}},
	}}
	loop := NewLoop(fr, "test-model", nil)
	loop.SetContextBudget(500, false) // 声明上限（非默认值）：旧版此处硬报错

	ch, err := loop.Run(context.Background(), ledger, big+"（第七轮）")
	if err != nil {
		t.Fatalf("压缩后应正常开局，仍报错：%v", err)
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
	// 账本里恰好落了一次压缩事件
	if n := countEvents(t, dir, session.EventCompaction); n != 1 {
		t.Fatalf("compaction events = %d, want 1", n)
	}
	// 第 1 次模型调用是摘要（system 为压缩器说明、user 是转录）；第 2 次是正式对话，
	// 消息以【历史摘要】开头且不再包含被归档的早期轮次。
	fr.mu.Lock()
	if len(fr.reqs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(fr.reqs))
	}
	sum := fr.reqs[0]
	main := fr.reqs[1]
	fr.mu.Unlock()
	if len(sum.Messages) != 2 || !strings.Contains(sum.Messages[0].Content, "压缩器") {
		t.Fatalf("第一次调用应是摘要请求：%+v", sum.Messages)
	}
	if len(main.Messages) == 0 || !strings.HasPrefix(main.Messages[0].Content, compactionMarker) {
		t.Fatalf("正式请求应以压缩摘要开头：%+v", main.Messages[:1])
	}
	if strings.Contains(main.Messages[0].Content, "（第一轮）") {
		t.Fatal("摘要里不应残留截点前的原文轮次")
	}
}

// C-AGT-10：摘要调用失败 → 回退旧行为（明确终态报超限），不静默发一个装不下的请求。
func TestAgent_OverflowCompactionFailureFallsBack(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	big := strings.Repeat("早期讨论内容。", 60) // 每轮 240 字：3 轮装不进 500 tok 预算
	for i := 0; i < 2; i++ {             // 再算上 Run 的新用户消息共 3 轮：不可压缩（无模型调用）
		if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": big}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": "收到"}); err != nil {
			t.Fatal(err)
		}
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{
		{{EndReason: llm.EndError, Err: context.DeadlineExceeded}},
	}}
	loop := NewLoop(fr, "test-model", nil)
	loop.SetContextBudget(800, false)

	_, err := loop.Run(context.Background(), ledger, big)
	if err == nil || !strings.Contains(err.Error(), "超过渠道上限") {
		t.Fatalf("want 超限错误, got %v", err)
	}
	if n := countEvents(t, dir, session.EventCompaction); n != 0 {
		t.Fatalf("压缩失败不应落账, compaction events = %d", n)
	}
}

// 压缩不可行（用户轮不足）→ 旧行为直接报错，不发起摘要调用。
func TestAgent_OverflowTooFewTurnsFallsBack(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	big := strings.Repeat("长内容，", 4000)
	for i := 0; i < 2; i++ { // 再算上 Run 的新用户消息共 3 轮：不可压缩（无模型调用）
		if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": big}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": "收到"}); err != nil {
			t.Fatal(err)
		}
	}
	fr := &fakeRuntime{script: [][]llm.StreamChunk{}}
	loop := NewLoop(fr, "test-model", nil)
	loop.SetContextBudget(500, false)

	_, err := loop.Run(context.Background(), ledger, big)
	if err == nil || !strings.Contains(err.Error(), "超过渠道上限") {
		t.Fatalf("want 超限错误, got %v", err)
	}
	if fr.requestCount() != 0 {
		t.Fatalf("不应发起任何模型调用，实际 %d", fr.requestCount())
	}
}

func compactionMessagesText(msgs []llm.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}
