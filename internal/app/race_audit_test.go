package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// 混合并发审计（0.0.12 稳定批）：并行发送 + 管理操作交错。
// 上个会话的审计提到"数据竞争"P0 但无定义落盘——这里用 -race 压力找证据。
// 焦点：Send 进行中触发 SetWorkspace（内部 activate 会动共享 pool/gateway）
// 与账本读取（Replay/ListSessions），看是否串流、丢终态或竞争。
// 本测试保留为回归：并行下管理操作必须全部安全。
func TestChatService_ParallelSendWithConfigChurn(t *testing.T) {
	const sessions = 4
	var mu sync.Mutex
	inflight := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inflight++
		mu.Unlock()
		defer func() {
			mu.Lock()
			inflight--
			mu.Unlock()
		}()
		w.Header().Set("Content-Type", "text/event-stream")
		// 长回复：30 个增量拉长写账本的时间窗，放大与管理操作的交错
		for i := 0; i < 30; i++ {
			fmt.Fprintf(w, "data: %s\n\n", fmt.Sprintf(`{"choices":[{"delta":{"content":"增量%d\n"}}]}`, i))
		}
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	s := newChannelService(t, Config{
		DataDir: t.TempDir(), BaseURL: srv.URL, APIKey: "sk-t", Model: "m1",
	})
	ws := t.TempDir()
	if err := s.SetWorkspace(ws); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	type outcome struct {
		sid  string
		end  llm.EndReason
		err  error
		text int
	}
	outcomes := make(chan outcome, sessions)
	for i := 0; i < sessions; i++ {
		sid := fmt.Sprintf("s-race-%d", i)
		wg.Add(1)
		go func(sid string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			ch, err := s.Send(ctx, sid, "并发压力提问")
			if err != nil {
				outcomes <- outcome{sid: sid, err: err}
				return
			}
			o := outcome{sid: sid}
			for c := range ch {
				if c.Delta != "" {
					o.text += len([]rune(c.Delta))
				}
				if c.EndReason != llm.EndNone {
					o.end, o.err = c.EndReason, c.Err
				}
			}
			outcomes <- o
		}(sid)
	}

	// 管理操作交错：与发送并行做 20 轮读取 + 工作区切换（触发 activate）+ 误删拒绝
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			_, _ = s.Replay("s-race-0")
			_, _ = s.ListSessions()
			_ = s.SetWorkspace("")
			_ = s.SetWorkspace(ws)
			_ = s.DeleteSession("s-not-running") // 未在运行的删除必须安全
			time.Sleep(2 * time.Millisecond)
		}
	}()
	wg.Wait()
	close(outcomes)

	bySid := map[string]outcome{}
	for o := range outcomes {
		bySid[o.sid] = o
	}
	for i := 0; i < sessions; i++ {
		sid := fmt.Sprintf("s-race-%d", i)
		o, ok := bySid[sid]
		if !ok {
			t.Fatalf("%s 没有终态结果（丢流）", sid)
		}
		if o.err != nil {
			t.Fatalf("%s 轮次失败：%v", sid, o.err)
		}
		if o.end != llm.EndDone {
			t.Fatalf("%s 终态 = %v，want done", sid, o.end)
		}
		if o.text == 0 {
			t.Fatalf("%s 增量文本为空（串流或丢失）", sid)
		}
	}
}
