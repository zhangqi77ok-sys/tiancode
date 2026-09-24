package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
)

// C-APP-1：持久化失败（账本目录创建失败）必须上抛，禁止吞错。
func TestChatService_PersistErrorPropagates(t *testing.T) {
	// 用一个同名"文件"占据目录位，使 MkdirAll 必然失败
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := NewChatService(Config{
		DataDir: blocker,
		BaseURL: "http://127.0.0.1:1", // 不会真正发请求：持久化先于网络
		APIKey:  "k",
		Model:   "m",
		WorkDir: ".",
		// 隔离渠道文件：绝不读写用户真实的 %APPDATA%\tiancode\channels.json
		ChannelsPath: filepath.Join(t.TempDir(), "channels.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(context.Background(), "s1", "hi"); err == nil {
		t.Fatal("persist failure must propagate to caller")
	}
}

// 装配校验：缺关键配置必须在构造期失败（fail-fast），不留半装配实例。
func TestChatService_ConfigValidation(t *testing.T) {
	// 注意：BaseURL/Model 不再是必填（渠道配置是唯一事实源，且允许先启动后配置渠道）；
	// 只有数据目录与工作区是硬前提。
	cases := []struct {
		name string
		cfg  Config
	}{
		{"missing datadir", Config{WorkDir: "."}},
		{"missing workdir", Config{DataDir: t.TempDir()}},
		// 不存在的目录必须早失败：否则启动看似正常，直到第一次写文件才报错（实机踩过）
		{"nonexistent workdir", Config{DataDir: t.TempDir(), WorkDir: filepath.Join(t.TempDir(), "nope")}},
	}
	for _, tc := range cases {
		if _, err := NewChatService(tc.cfg); err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
	}
}

// Replay 只投影已确认锚点：被取消轮次的 delta 不进历史（与 agent 语义一致）。
func TestChatService_ReplayProjectsAnchors(t *testing.T) {
	dir := t.TempDir()
	l, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "u1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventAssistantDelta, map[string]any{"text": "partial", "thinking": ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventAssistantMsg, map[string]string{"text": "a1"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := NewChatService(Config{
		DataDir: dir, BaseURL: "http://x", APIKey: "k", Model: "m", WorkDir: ".",
		ChannelsPath: filepath.Join(t.TempDir(), "channels.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() // Windows: 未关闭句柄会阻止 TempDir 清理
	msgs, err := s.Replay("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (delta 不投影)", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "u1" {
		t.Fatalf("msgs[0] = %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "a1" {
		t.Fatalf("msgs[1] = %+v", msgs[1])
	}
}

// C-APP-2：用户中断 → 恰好一个 EndCancelled 终态，且账本保留取消前已产生的事件。
//
// 为什么必须锁这条：取消链路横跨 core/agent（ctx 取消语义）与壳层（Bind.Stop 触发取消），
// 是整个产品里唯一"用户按下按钮就要立刻停住"的路径。若有人把增量改成"先上抛 UI 再落账本"，
// 取消瞬间的部分输出就会静默丢失——界面照样显示"已取消"，用户却再也找不回那半段内容。
// 因此断言分两层：终态唯一且为 EndCancelled（不骗 UI）；账本磁盘内容含 user_message 与
// 取消前的 assistant_delta（不丢数据）。后者直接读文件，不经任何内存态。
func TestChatService_CancelKeepsEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// 先吐一个增量（构造"已产生事件"），再挂住直到客户端断开，模拟长推理。
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"部分输出"}}]}`)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	s := newChannelService(t, Config{
		DataDir: dataDir, BaseURL: srv.URL, APIKey: "sk-t", Model: "m1",
	})

	ctx, cancel := context.WithCancel(context.Background())
	// vet 的 lostcancel 检查要求 cancel 在所有路径上都被用到：早期 t.Fatal 退出时
	// 也必须释放，否则测试自身就会泄漏 context。
	defer cancel()
	ch, err := s.Send(ctx, "s1", "开始")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var terminals []llm.StreamChunk
	var deltas int

	// 先真正收到一个增量再中断。为什么不在"上游已 flush"时就取消：此刻客户端可能仍在
	// 流前建连阶段，取消会走流前失败分支——那不是本契约要锁的场景。真实场景是
	// "用户看到部分输出，然后按下中断"，所以必须以消费方观察到 delta 为中断时点。
	for cancelled := false; !cancelled; {
		select {
		case c, ok := <-ch:
			if !ok {
				t.Fatal("流在收到首个增量前关闭，无法构造进行中的轮次")
			}
			if c.EndReason != llm.EndNone {
				t.Fatalf("中断前出现终态 %v (err=%v)", c.EndReason, c.Err)
			}
			if c.Delta != "" {
				deltas++
				cancel() // 等同 UI 点"中断"：壳层 Bind.Stop 取消的也是同一个 ctx
				cancelled = true
			}
		case <-time.After(5 * time.Second):
			t.Fatal("5s 内未收到首个增量")
		}
	}

	deadline := time.After(10 * time.Second)
drain:
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				break drain
			}
			switch {
			case c.EndReason != llm.EndNone:
				terminals = append(terminals, c)
			case c.Delta != "":
				deltas++
			}
		case <-deadline:
			t.Fatalf("取消后流未收束：terminals=%d deltas=%d", len(terminals), deltas)
		}
	}

	if len(terminals) != 1 {
		t.Fatalf("终态块数 = %d, want 1（终态互斥且唯一）", len(terminals))
	}
	if terminals[0].EndReason != llm.EndCancelled {
		t.Fatalf("EndReason = %v (err=%v), want EndCancelled —— 主动中断不得被上报成错误",
			terminals[0].EndReason, terminals[0].Err)
	}
	if deltas == 0 {
		t.Fatal("未观察到任何增量，测试前提不成立")
	}

	// 账本必须保留已产生事件（直接读盘校验，不依赖内存态）
	raw, err := os.ReadFile(filepath.Join(dataDir, "s1.jsonl"))
	if err != nil {
		t.Fatalf("读账本: %v", err)
	}
	ledger := string(raw)
	if !strings.Contains(ledger, "user_message") {
		t.Error("账本缺少 user_message：用户输入被丢弃")
	}
	if !strings.Contains(ledger, "assistant_delta") || !strings.Contains(ledger, "部分输出") {
		t.Errorf("账本缺少取消前已产生的 assistant_delta：部分输出被丢弃\n%s", ledger)
	}

	// 取消的轮次不写锚点，故不进入历史（Replay 只投影锚点）
	msgs, err := s.Replay("s1")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("Replay = %+v, want 仅 1 条 user（取消轮次不进历史）", msgs)
	}
}
