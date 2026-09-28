package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"tiancode/internal/core/agent"
)

// 问答配对：Ask 发事件并阻塞，ResolveAsk 回流答案；已处理 ID 重复答复必须报错。
func TestChatService_ResolveAskFlow(t *testing.T) {
	dir := t.TempDir()
	s, err := NewChatService(Config{
		DataDir: dir, WorkDir: ".",
		ChannelsPath: filepath.Join(t.TempDir(), "channels.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	events := make(chan AskEvent, 1)
	s.SetAskHandler(func(e AskEvent) { events <- e })

	ak := &uiAsker{svc: s}
	type result struct {
		answer string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		answer, err := ak.Ask(context.Background(), agent.AskRequest{
			Question: "选哪个方案？", Options: []string{"方案 A", "方案 B"},
		})
		done <- result{answer, err}
	}()

	var ev AskEvent
	select {
	case ev = <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("超时未收到问答事件")
	}
	if ev.ID == "" || ev.Question != "选哪个方案？" || len(ev.Options) != 2 {
		t.Fatalf("问答事件不符：%+v", ev)
	}
	if err := s.ResolveAsk(ev.ID, "方案 A"); err != nil {
		t.Fatalf("ResolveAsk: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.answer != "方案 A" {
			t.Fatalf("ask 返回 %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时未返回")
	}
	// 已处理的 ID 再答 → 明确报错（与审批同纪律）
	if err := s.ResolveAsk(ev.ID, "再次回答"); err == nil {
		t.Fatal("重复答复应报错")
	}
}
