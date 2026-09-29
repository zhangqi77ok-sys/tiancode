package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// 零事件看门狗（0.2.29）：上游从不回应时（代理黑洞/连接挂死——这些场景下
// 响应头超时与流层空闲看门狗都管不到），看门狗必须自动取消并给出明确超时
// 错误，绝不允许"永久运行中无任何反馈"。
func TestChatService_ZeroEventWatchdog(t *testing.T) {
	old := firstEventTimeout
	// 窗口 1.5s：CI 的 2 核 runner 上全包并行时，看门狗 goroutine 自身也要排队
	// （实测 400ms 窗口在全量 go test 的负载下会偶发超时——窗口是给"调度延迟"
	// 留的余量，不是要验证的量级；要验证的是"必然触发"而非"触发得多快"）。
	firstEventTimeout = 1500 * time.Millisecond
	t.Cleanup(func() { firstEventTimeout = old })

	// finished：测试本体结束（看门狗已断言完）后让 handler 立即退场——
	// 否则 srv.Close 会干等自限时（15s），测试时长全耗在清理上。
	finished := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 永不回应（模拟黑洞/CONNECT 挂死）。自限时退出：httptest 服务端对
		// "未开始读写的挂起 handler"感知客户端断连的时机不可靠，靠 r.Context()
		// 会让 srv.Close 在测试收尾时长时间阻塞。**必须远大于断言 deadline**：
		// 若两者接近，handler 超时产生的 EOF 终态会与测试 deadline 竞速，
		// 失败信息变成误导性的断言文本（实测 5s==5s 翻过车）。
		select {
		case <-r.Context().Done():
		case <-finished:
		case <-time.After(15 * time.Second):
		}
	}))
	t.Cleanup(func() { close(finished); srv.Close() })
	s := newChannelService(t, Config{})
	if _, err := s.AddChannel(llm.Channel{
		Name: "blk", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "m1", APIKey: "k",
	}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	ch, err := s.Send(context.Background(), "s-watchdog", "hi")
	if err != nil {
		// 流建立前就被看门狗取消：错误必须是人类可读的"响应超时"，不是裸 context canceled
		if !strings.Contains(err.Error(), "响应超时") {
			t.Fatalf("err = %v, want 含响应超时", err)
		}
		if time.Since(start) > 10*time.Second {
			t.Fatalf("看门狗应在 1.5s 量级触发，实际 %v", time.Since(start))
		}
		return
	}
	defer func() {
		for range ch { // 排干（测试失败路径也要释放）
		}
	}()
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
				t.Fatalf("看门狗应在 1.5s 量级触发，实际 %v", time.Since(start))
			}
			return
		case <-deadline:
			t.Fatal("10 秒内未见终态：看门狗未生效，用户将无限期挂在运行中")
		}
	}
}

// 反向纪律：已有可见活动的轮次（模型在输出）绝不能被看门狗误杀。
// 慢速上游逐字输出，总时长超过看门狗阈值——只要持续有块就活到最后。
func TestChatService_WatchdogDoesNotKillActiveStream(t *testing.T) {
	old := firstEventTimeout
	// 窗口 1.5s（理由见 ZeroEventWatchdog）：首块要穿过 httptest→provider→gateway
	// →runtime→agent→分发 共 5 跳 goroutine，CI 慢机上 300ms 窗口会被调度延迟击穿
	// （实测首块迟到 → sawEvent 尚未置位 → 看门狗把活跃流当成零事件杀掉）。
	// 节奏 250ms ≪ 窗口：始终有活动，窗口翻倍也不该触发。
	firstEventTimeout = 1500 * time.Millisecond
	t.Cleanup(func() { firstEventTimeout = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// 每 250ms 一块、共 6 块：总时长 1.5s，但始终有活动
		for i := 0; i < 6; i++ {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(250 * time.Millisecond)
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	s := newChannelService(t, Config{})
	if _, err := s.AddChannel(llm.Channel{
		Name: "slow", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "m1", APIKey: "k",
	}); err != nil {
		t.Fatal(err)
	}

	ch, err := s.Send(context.Background(), "s-active", "hi")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for range ch {
		}
	}()
	deadline := time.After(5 * time.Second)
	var text strings.Builder
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				t.Fatal("流关闭但未收到终态")
			}
			text.WriteString(c.Delta)
			if c.EndReason != llm.EndNone {
				if c.EndReason != llm.EndDone {
					t.Fatalf("活跃流被误杀：%v/%v", c.EndReason, c.Err)
				}
				if text.Len() != 6 {
					t.Fatalf("增量数 = %d, want 6（完整到达）", text.Len())
				}
				return
			}
		case <-deadline:
			t.Fatal("5 秒内未见终态")
		}
	}
}
