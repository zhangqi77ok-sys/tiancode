package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
)

// ---- 端到端实测（0.0.06 用户反馈"功能要实际测试过"）：不再只测解析函数本身，
// 而是从 SendWithAttachments 一路打到 mock 上游，断言**模型真实收到的请求体**。 ----

// captureUpstream 是可编程的 OpenAI 兼容 SSE 上游：记录每个请求体（模型收到的
// 完整上下文），按脚本依次返回流式回复。
type captureUpstream struct {
	mu       sync.Mutex
	requests []string // 原始请求体
	script   []string // 每轮的 SSE 文本回复（依次消费）
}

func (c *captureUpstream) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		i := len(c.requests)
		c.requests = append(c.requests, string(body))
		reply := "收到"
		if i < len(c.script) {
			reply = c.script[i]
		}
		c.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"" + reply + "\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}
}

// lastUserContentOf 从请求体 JSON 里取最后一条 user 消息的文本内容（含 parts 拼接）。
func lastUserContentOf(t *testing.T, body string) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content json.RawMessage
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("请求体解析失败：%v\n%s", err, body)
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != "user" {
			continue
		}
		// content 可能是字符串或 parts 数组
		var s string
		if err := json.Unmarshal(req.Messages[i].Content, &s); err == nil {
			return s
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(req.Messages[i].Content, &parts); err == nil {
			var b strings.Builder
			for _, p := range parts {
				b.WriteString(p.Text)
			}
			return b.String()
		}
		return string(req.Messages[i].Content)
	}
	t.Fatalf("请求里没有 user 消息：%s", body)
	return ""
}

// 0.0.11 用户实测"输入 @ 选了文件没有任何效果"：修复后，消息文本里的 @相对路径
// 必须把文件内容**真实带进模型上下文**——首轮如此，下一轮重放历史仍要如此。
func TestE2E_AtReference_ReachesModelContext(t *testing.T) {
	root := t.TempDir()
	writeFile := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("main.go", "package main // E2E-NEEDLE-7f3a")
	writeFile("docs/guide.md", "# guide E2E-NEEDLE-doc")

	up := &captureUpstream{script: []string{"好的第一轮", "好的第二轮"}}
	srv := httptest.NewServer(up.handler())
	t.Cleanup(srv.Close)

	s := newChannelService(t, Config{WorkDir: root})
	if _, err := s.AddChannel(llm.Channel{
		Name: "mock", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "m1", APIKey: "k",
	}); err != nil {
		t.Fatal(err)
	}
	sid := "s-e2e-at"

	// 第一轮：消息里带 @引用
	ch, err := s.SendWithAttachments(context.Background(), sid, "请分析 @main.go 和 @docs/guide.md", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := drainTerminal(ch, 10*time.Second); err != nil {
		t.Fatalf("第一轮未正常收束：%v", err)
	}

	if len(up.requests) != 1 {
		t.Fatalf("应恰有 1 个上游请求，实际 %d", len(up.requests))
	}
	first := lastUserContentOf(t, up.requests[0])
	if !strings.Contains(first, "E2E-NEEDLE-7f3a") {
		t.Fatalf("@main.go 的内容没有进入模型上下文：\n%s", first)
	}
	if !strings.Contains(first, "E2E-NEEDLE-doc") {
		t.Fatalf("@docs/guide.md 的内容没有进入模型上下文：\n%s", first)
	}

	// 第二轮：新消息（无 @）——重放历史时上一轮的 @引用内容仍必须可见
	ch, err = s.Send(context.Background(), sid, "继续")
	if err != nil {
		t.Fatal(err)
	}
	if err := drainTerminal(ch, 10*time.Second); err != nil {
		t.Fatalf("第二轮未正常收束：%v", err)
	}
	if len(up.requests) != 2 {
		t.Fatalf("应恰有 2 个上游请求，实际 %d", len(up.requests))
	}
	second := up.requests[1]
	if !strings.Contains(second, "E2E-NEEDLE-7f3a") {
		t.Fatalf("下一轮重放历史丢失了 @引用内容：\n%s", second)
	}
}

// 0.0.06 用户实测"任务清单一直显示旧的"——账本级生命周期：清单之后出现新的
// 用户消息（= 新任务开始）后，重放投影不再带旧清单；仍是当前任务时照常恢复。
func TestE2E_TodoProjection_Lifecycle(t *testing.T) {
	s := newChannelService(t, Config{})
	dataDir := s.cfg.DataDir

	appendEvent := func(sid string, kind session.EventKind, payload map[string]any) {
		t.Helper()
		l, err := session.OpenLedger(dataDir, sid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.Append(kind, payload); err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	user := func(sid, text string) {
		appendEvent(sid, session.EventUserMessage, map[string]any{"text": text})
	}
	todo := func(sid string, items []map[string]any) {
		appendEvent(sid, session.EventTodo, map[string]any{"items": items})
	}
	todoRoles := func(msgs []ChatMessage) []string {
		var roles []string
		for _, m := range msgs {
			if m.Role == "todo" {
				roles = append(roles, m.Content)
			}
		}
		return roles
	}

	t.Run("清单之后有新用户消息：重放不再带旧清单", func(t *testing.T) {
		sid := "s-e2e-todo-stale"
		user(sid, "任务A开始")
		todo(sid, []map[string]any{{"text": "步骤1", "status": "in_progress"}})
		user(sid, "换个话题") // 新任务开始
		msgs, err := s.Replay(sid)
		if err != nil {
			t.Fatal(err)
		}
		if got := todoRoles(msgs); len(got) != 0 {
			t.Fatalf("旧清单复活了：%v", got)
		}
	})

	t.Run("清单仍是当前任务：重放恢复最新快照", func(t *testing.T) {
		sid := "s-e2e-todo-live"
		user(sid, "任务B开始")
		todo(sid, []map[string]any{{"text": "步骤1", "status": "pending"}})
		todo(sid, []map[string]any{{"text": "步骤1", "status": "done"}})
		msgs, err := s.Replay(sid)
		if err != nil {
			t.Fatal(err)
		}
		got := todoRoles(msgs)
		if len(got) != 1 || !strings.Contains(got[0], `"done"`) {
			t.Fatalf("当前任务清单应恢复最新快照：%v", got)
		}
	})
}

// drainTerminal 读到终态块为止（通道随后关闭）。
func drainTerminal(ch <-chan llm.StreamChunk, timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return nil
			}
			if c.EndReason != llm.EndNone {
				if c.EndReason != llm.EndDone {
					return c.Err
				}
				for range ch { // 终态后清空剩余（契约：随后关闭）
				}
				return nil
			}
		case <-deadline:
			return context.DeadlineExceeded
		}
	}
}
