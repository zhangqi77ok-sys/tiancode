package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// 0.0.07 实机复现（用户截图：ask 答复后"运行中"卡 38 分钟）：
// 模型第一轮调 ask_user → 用户答复 → 模型带上下文继续推理 → 上游黑洞。
// 此前 watchAsker 在"进入等待"时 mark 了 sawEvent，答复后从不重置——零事件
// 看门狗从此永久豁免，黑洞场景没有任何层收束，界面无限期"运行中"。
// 修复：答复返回后 reset 重新计时。本测试在旧实现下必然超时失败。
func TestChatService_WatchdogResetsAfterAskAnswer(t *testing.T) {
	old := firstEventTimeout
	firstEventTimeout = 1500 * time.Millisecond
	t.Cleanup(func() { firstEventTimeout = old })

	var calls atomic.Int64
	start0 := time.Now()
	finished := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		t.Logf("UPSTREAM CALL #%d at %v", n, time.Since(start0))
		if n == 1 {
			// 第一轮：正常流——模型调用 ask_user（问题走 UI 答复通道）
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"a1\",\"function\":{\"name\":\"ask_user\",\"arguments\":\"{\\\"question\\\":\\\"这是什么？\\\"}\"}}]}}]}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		// 答复后的第二轮：黑洞（连响应头都不发）——复现"答复后上游挂死"
		select {
		case <-r.Context().Done():
		case <-finished:
		case <-time.After(15 * time.Second):
		}
	}))
	t.Cleanup(func() { close(finished); srv.Close() })

	s := newChannelService(t, Config{})
	if _, err := s.AddChannel(llm.Channel{
		Name: "ask-blk", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "m1", APIKey: "k",
	}); err != nil {
		t.Fatal(err)
	}

	// 捕获问答事件并自动答复（模拟用户看到卡片后回复）
	gotAsk := make(chan string, 1)
	s.SetAskHandler(func(ev AskEvent) {
		gotAsk <- ev.ID
	})

	start := time.Now()
	ch, err := s.Send(context.Background(), "s-ask-reset", "看图回答这是什么")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for range ch {
		}
	}()

	// 等 ask 事件出现并答复
	var askID string
	select {
	case askID = <-gotAsk:
	case <-time.After(5 * time.Second):
		t.Fatal("5 秒内未收到问答事件（上游第一轮未返回 ask_user）")
	}
	t.Logf("ASK EVENT at %v, resolving", time.Since(start0))
	if err := s.ResolveAsk(askID, "三级吊装作业许可证"); err != nil {
		t.Fatalf("ResolveAsk: %v", err)
	}

	// 答复后的第二轮是黑洞：看门狗必须在 reset 后的新窗口内收束轮次。
	deadline := time.After(10 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				t.Fatal("流关闭但未收到终态")
			}
			if c.EndReason == llm.EndNone {
				continue
			}
			if c.EndReason != llm.EndError || c.Err == nil || !strings.Contains(c.Err.Error(), "响应超时") {
				t.Fatalf("终态 = %v/%v, want EndError/响应超时", c.EndReason, c.Err)
			}
			if time.Since(start) > 10*time.Second {
				t.Fatalf("答复后看门狗应在 1.5s 量级收束，实际 %v", time.Since(start))
			}
			return
		case <-deadline:
			t.Fatal("答复后 10 秒未见终态：看门狗 reset 未生效——用户将无限期卡在运行中（实机 38 分钟复现）")
		}
	}
}
