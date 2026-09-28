package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/adaptors"
	"tiancode/internal/platform/channels"
)

const sseOK = "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

// newUpstream 构造计数上游：status>0 时每次都返回该错误码，否则返回 OpenAI SSE 成功流。
// 记录调用次数、Authorization 头与请求体里的 model（供断言）。
func newUpstream(t *testing.T, status int) (*httptest.Server, *int32, *string, *string) {
	t.Helper()
	var calls int32
	var lastAuth, lastModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		lastAuth = r.Header.Get("Authorization")
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastModel = body.Model
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"upstream down"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseOK))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &lastAuth, &lastModel
}

func newGateway(t *testing.T, chs ...channels.Channel) *Gateway {
	t.Helper()
	reg := adaptors.NewRegistry()
	if err := reg.Register(adaptors.OpenAI{}); err != nil {
		t.Fatal(err)
	}
	pool := channels.NewPool(t.TempDir() + "/channels.json")
	for _, c := range chs {
		if err := pool.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	return New(pool, reg)
}

func chanOf(id, model string, prio int, extra ...func(*channels.Channel)) channels.Channel {
	c := channels.Channel{ID: id, Type: "openai", Name: id, Credential: "key-" + id,
		Models: []string{model}, Groups: []string{channels.DefaultGroup},
		Status: channels.StatusEnabled, Priority: prio}
	for _, f := range extra {
		f(&c)
	}
	return c
}

func collect(t *testing.T, ch <-chan llm.StreamChunk) (string, llm.StreamChunk) {
	t.Helper()
	var text strings.Builder
	var terminal llm.StreamChunk
	for c := range ch {
		if c.EndReason != llm.EndNone {
			terminal = c
			continue
		}
		text.WriteString(c.Delta)
	}
	return text.String(), terminal
}

// 完成标准：高 priority 渠道失败后，重试落到低 priority 渠道（不同渠道），调用方无感。
func TestGateway_RetryFallsToLowerPriority(t *testing.T) {
	srvA, callsA, _, _ := newUpstream(t, http.StatusServiceUnavailable)
	srvB, callsB, _, _ := newUpstream(t, 0)
	g := newGateway(t,
		chanOf("a", "m", 100, func(c *channels.Channel) { c.BaseURL = srvA.URL }),
		chanOf("b", "m", 50, func(c *channels.Channel) { c.BaseURL = srvB.URL }),
	)
	text, terminal := collect(t, mustStream(t, g, "m"))
	if text != "hi" || terminal.EndReason != llm.EndDone {
		t.Fatalf("text=%q terminal=%+v", text, terminal)
	}
	if atomic.LoadInt32(callsA) != 1 || atomic.LoadInt32(callsB) != 1 {
		t.Fatalf("calls a=%d b=%d（A 只打一次，重试必须排除它）", atomic.LoadInt32(callsA), atomic.LoadInt32(callsB))
	}
}

// 完成标准：model_mapping 把下游模型名改写为上游真实模型名，调用方请求不变。
func TestGateway_ModelMapping(t *testing.T) {
	srv, calls, _, lastModel := newUpstream(t, 0)
	g := newGateway(t, chanOf("a", "gpt-x", 100, func(c *channels.Channel) {
		c.BaseURL = srv.URL
		c.ModelMapping = map[string]string{"gpt-x": "up-model"}
	}))
	text, terminal := collect(t, mustStream(t, g, "gpt-x"))
	if text != "hi" || terminal.EndReason != llm.EndDone {
		t.Fatalf("text=%q terminal=%+v", text, terminal)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("calls = %d", atomic.LoadInt32(calls))
	}
	if *lastModel != "up-model" {
		t.Fatalf("上游收到的 model = %q, want up-model", *lastModel)
	}
}

// 完成标准：多凭证渠道中一条凭证 401 被禁用，其余凭证继续服务（同档重试，不降档不换渠道）。
func TestGateway_MultiCredentialFailover(t *testing.T) {
	var calls int32
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		auths = append(auths, r.Header.Get("Authorization"))
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseOK))
	}))
	defer srv.Close()

	g := newGateway(t, chanOf("a", "m", 100, func(c *channels.Channel) {
		c.BaseURL = srv.URL
		c.Credential = "k1\nk2"
		c.AutoBan = true
	}))
	text, terminal := collect(t, mustStream(t, g, "m"))
	if text != "hi" || terminal.EndReason != llm.EndDone {
		t.Fatalf("text=%q terminal=%+v", text, terminal)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("calls = %d, want 2（k1 失败后 k2 续上）", atomic.LoadInt32(&calls))
	}
	if len(auths) != 2 || auths[0] != "Bearer k1" || auths[1] != "Bearer k2" {
		t.Fatalf("auths = %v", auths)
	}
	ch, _ := g.Pool.Get("a")
	if ch.Status != channels.StatusEnabled {
		t.Fatalf("status = %q（其余凭证可用时渠道不得禁用）", ch.Status)
	}
	if !ch.CredentialState.Disabled[0] || ch.CredentialState.Disabled[1] {
		t.Fatalf("凭证状态不符：%v", ch.CredentialState.Disabled)
	}
}

// auto_ban：单凭证渠道故障 → auto_disabled，且后续选路明确报无可用渠道。
func TestGateway_AutoBanSingleKey(t *testing.T) {
	srv, calls, _, _ := newUpstream(t, http.StatusServiceUnavailable)
	g := newGateway(t, chanOf("a", "m", 100, func(c *channels.Channel) {
		c.BaseURL = srv.URL
		c.AutoBan = true
	}))
	_, terminal := collect(t, mustStream(t, g, "m"))
	if terminal.EndReason != llm.EndError {
		t.Fatalf("terminal = %+v, want EndError", terminal)
	}
	if atomic.LoadInt32(calls) < 1 {
		t.Fatal("至少应尝试一次")
	}
	ch, _ := g.Pool.Get("a")
	if ch.Status != channels.StatusAutoDisabled {
		t.Fatalf("auto_ban 后 status = %q", ch.Status)
	}
	if _, err := g.Pool.Select(channels.Selection{Model: "m"}); !errors.Is(err, channels.ErrNoChannel) {
		t.Fatalf("禁用后应 ErrNoChannel，got %v", err)
	}
}

// 无可用渠道的错误必须可读可执行：用户要能知道该做什么。
// 原始错误 "无可用渠道：default / m（retry=0）" 让应用表现为"发消息毫无反应"（实机反馈）。
func TestGateway_NoChannelErrorIsActionable(t *testing.T) {
	// 1) 未配置任何渠道 → 引导新增
	g := newGateway(t)
	_, terminal := collect(t, mustStream(t, g, "m"))
	if terminal.EndReason != llm.EndError {
		t.Fatalf("terminal = %+v, want EndError", terminal)
	}
	if !strings.Contains(terminal.Err.Error(), "新增渠道") {
		t.Fatalf("未配置渠道应引导新增，got %v", terminal.Err)
	}

	// 2) 渠道被 auto_ban 自动禁用 → 点名渠道 + 给出恢复动作
	srv, _, _, _ := newUpstream(t, http.StatusServiceUnavailable)
	g2 := newGateway(t, chanOf("ss2a", "m", 100, func(c *channels.Channel) {
		c.BaseURL = srv.URL
		c.AutoBan = true
	}))
	collect(t, mustStream(t, g2, "m")) // 第一次：上游故障 → auto_ban
	_, terminal2 := collect(t, mustStream(t, g2, "m"))
	msg := terminal2.Err.Error()
	if !strings.Contains(msg, "ss2a") || !strings.Contains(msg, "自动禁用") {
		t.Fatalf("错误应点名渠道与自动禁用状态，got %v", terminal2.Err)
	}
	if !strings.Contains(msg, "启用") {
		t.Fatalf("错误应给出恢复动作，got %v", terminal2.Err)
	}
}

// 渠道不含所请求模型时必须点名两边的模型名（用户改模型名时唯一的线索）。
func TestGateway_NoChannelErrorNamesMissingModel(t *testing.T) {
	g := newGateway(t, chanOf("a", "old-model", 100))
	_, terminal := collect(t, mustStream(t, g, "new-model"))
	if terminal.EndReason != llm.EndError {
		t.Fatalf("terminal = %+v, want EndError", terminal)
	}
	msg := terminal.Err.Error()
	if !strings.Contains(msg, "new-model") || !strings.Contains(msg, "old-model") {
		t.Fatalf("应点名缺失模型与渠道现有模型，got %v", msg)
	}
}

func mustStream(t *testing.T, g *Gateway, model string) <-chan llm.StreamChunk {
	t.Helper()
	ch, err := g.StreamChat(context.Background(), llm.ChatRequest{
		Model: model, Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

var _ = fmt.Sprintf
