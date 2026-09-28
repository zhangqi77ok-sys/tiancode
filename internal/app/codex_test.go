package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// codexSSE 是最小 Codex 响应流（文本 + 终态）。
const codexSSE = `data: {"type":"response.created","response":{"status":"in_progress"}}` + "\n\n" +
	`data: {"type":"response.output_text.delta","delta":"pong"}` + "\n\n" +
	`data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n"

// 导入 Codex 凭证 → 自动建 codex 渠道 → 摘要回显 → 测试通道走通（模拟上游 SSE）。
func TestChatService_CodexImportAndTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			t.Errorf("路径 = %q（应打到 /responses）", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("chatgpt-account-id") != "acc-1" {
			t.Errorf("chatgpt-account-id = %q", r.Header.Get("chatgpt-account-id"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexSSE))
	}))
	t.Cleanup(srv.Close)

	s := newChannelService(t, Config{})
	credJSON := fmt.Sprintf(
		`{"type":"codex_oauth","access_token":"at-1","refresh_token":"rt-1","expires_at":%d,"email":"u@e.com","chatgpt_account_id":"acc-1","plan_type":"plus"}`,
		time.Now().Add(24*time.Hour).Unix())
	res, err := s.ImportCodexCredential(context.Background(), "", "", credJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.ChannelID == "" || !strings.Contains(res.Display, "u@e.com") {
		t.Fatalf("导入结果 = %+v", res)
	}

	// 渠道视图：codex 协议 + 默认模型
	views, activeID, err := s.Channels()
	if err != nil {
		t.Fatal(err)
	}
	var got llm.ChannelView
	for _, v := range views {
		if v.ID == res.ChannelID {
			got = v
		}
	}
	if got.Protocol != llm.ProtocolCodex || len(got.Models) != 1 || got.Models[0] != DefaultCodexModel {
		t.Fatalf("codex 渠道视图 = %+v", got)
	}
	if activeID != res.ChannelID {
		t.Fatalf("首个渠道应自动激活：activeID=%q", activeID)
	}
	if !got.HasKey {
		t.Fatal("OAuth 凭证应体现在 HasKey")
	}

	// 指向 mock 上游后测试通道（走真实链路：适配器 + SSE 解析）
	upd := llm.Channel{
		ID: res.ChannelID, Name: res.Name, Protocol: llm.ProtocolCodex,
		BaseURL: srv.URL, Models: []string{DefaultCodexModel},
	}
	if err := s.UpdateChannel(res.ChannelID, upd); err != nil {
		t.Fatal(err)
	}
	tr, err := s.TestChannel(context.Background(), res.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.OK || tr.Reply != "pong" {
		t.Fatalf("codex 测试结果 = %+v", tr)
	}

	// 摘要回显（渠道管理展示"已绑定 xx"，绝不回显 token）
	info := s.CodexCredentialOf(res.ChannelID)
	if !info.Bound || !strings.Contains(info.Display, "u@e.com") || strings.Contains(info.Display, "at-1") {
		t.Fatalf("凭证摘要 = %+v", info)
	}
}

// 请求前自动续期：临期凭证经刷新端点换新并写回池（TokenEndpoint 注入 mock）。
func TestChatService_CodexAutoRefresh(t *testing.T) {
	var refreshCalls int
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls++
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "rt-1" {
			t.Errorf("刷新请求 = %v", r.PostForm)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-new", "expires_in": 3600})
	}))
	t.Cleanup(tokenSrv.Close)

	s := newChannelService(t, Config{})
	s.codexClient.TokenEndpoint = tokenSrv.URL // 同包测试：注入刷新端点

	// 先用"远期过期"凭证建渠道（不触发导入期刷新）
	credJSON := fmt.Sprintf(
		`{"type":"codex_oauth","access_token":"at-1","refresh_token":"rt-1","expires_at":%d,"email":"u@e.com"}`,
		time.Now().Add(24*time.Hour).Unix())
	res, err := s.ImportCodexCredential(context.Background(), "", "", credJSON)
	if err != nil {
		t.Fatal(err)
	}
	if refreshCalls != 0 {
		t.Fatalf("远期凭证不应触发刷新：calls=%d", refreshCalls)
	}

	// 手动置换为临期凭证 → ensureCodexFresh 应刷新并写回
	near := fmt.Sprintf(
		`{"type":"codex_oauth","access_token":"at-old","refresh_token":"rt-1","expires_at":%d,"email":"u@e.com"}`,
		time.Now().Add(time.Minute).Unix())
	if err := s.pool.UpdateCredential(res.ChannelID, near); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureCodexFresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if refreshCalls != 1 {
		t.Fatalf("应恰好刷新一次：calls=%d", refreshCalls)
	}
	ch, ok := s.pool.Get(res.ChannelID)
	if !ok || !strings.Contains(ch.Credential, "at-new") {
		t.Fatalf("刷新后凭证未写回：%s", ch.Credential)
	}
	// 已新鲜：再次调用不产生刷新
	if err := s.ensureCodexFresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if refreshCalls != 1 {
		t.Fatalf("新鲜凭证不应重复刷新：calls=%d", refreshCalls)
	}
}

// 绑定到已有渠道（channelID 非空）：只更新凭证，不新建渠道。
func TestChatService_CodexBindUpdatesExisting(t *testing.T) {
	s := newChannelService(t, Config{})
	// 先建一条普通 openai 渠道
	added, err := s.AddChannel(llm.Channel{
		Name: "mine", Protocol: llm.ProtocolOpenAI, BaseURL: "https://gw/v1", Model: "m1", APIKey: "sk-old",
	})
	if err != nil {
		t.Fatal(err)
	}
	credJSON := fmt.Sprintf(
		`{"type":"codex_oauth","access_token":"at-9","refresh_token":"rt-9","expires_at":%d,"email":"x@y.com"}`,
		time.Now().Add(24*time.Hour).Unix())
	res, err := s.ImportCodexCredential(context.Background(), added.ID, "", credJSON)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created {
		t.Fatal("更新已有渠道不得报 Created")
	}
	if views, _, _ := s.Channels(); len(views) != 1 {
		t.Fatalf("不得新建渠道：共 %d 条", len(views))
	}
	info := s.CodexCredentialOf(added.ID)
	if !info.Bound || !strings.Contains(info.Display, "x@y.com") {
		t.Fatalf("绑定摘要 = %+v", info)
	}
}
