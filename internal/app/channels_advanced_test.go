package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/channels"
)

const testSSEOK = "data: {\"choices\":[{\"delta\":{\"content\":\"pong\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

// 渠道高级字段全链路：AddChannel/UpdateChannel 落盘并在脱敏视图回读。
// 0.2.19 前 IPC 绑定层丢字段（models/priority/weight/status/autoBan/映射），
// UI 上的设置从未生效——本测试把契约钉死在编排层之上。
func TestChatService_ChannelAdvancedFieldsRoundtrip(t *testing.T) {
	s := newChannelService(t, Config{})
	in := llm.Channel{
		Name: "main", Protocol: llm.ProtocolOpenAI, BaseURL: "https://gw/v1", Model: "m1", APIKey: "k1",
		Models: []string{"m1", "m2"}, Priority: 200, Weight: 5, AutoBan: true,
		ModelMapping:   map[string]string{"m1": "upstream-m1"},
		ParamOverride:  map[string]any{"temperature": 0.1},
		HeaderOverride: map[string]string{"x-e2e": "1"},
	}
	added, err := s.AddChannel(in)
	if err != nil {
		t.Fatal(err)
	}

	find := func() llm.ChannelView {
		t.Helper()
		views, _, err := s.Channels()
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range views {
			if v.ID == added.ID {
				return v
			}
		}
		t.Fatalf("渠道 %s 不在视图里", added.ID)
		return llm.ChannelView{}
	}

	v := find()
	if len(v.Models) != 2 || v.Models[1] != "m2" || v.Priority != 200 || v.Weight != 5 || !v.AutoBan {
		t.Fatalf("池能力面回读失败：%+v", v)
	}
	if v.ModelMapping["m1"] != "upstream-m1" || v.HeaderOverride["x-e2e"] != "1" {
		t.Fatalf("高级字段回读失败：%+v", v)
	}
	if v.CredentialCount != 1 || v.CredentialDisabled != 0 {
		t.Fatalf("凭证摘要 = %d 条 / %d 禁用, want 1/0", v.CredentialCount, v.CredentialDisabled)
	}

	// 更新：改状态/优先级/关闭 autoBan——全部必须生效
	upd := in
	upd.ID = added.ID
	upd.APIKey = "" // 留空 = 保持原密钥
	upd.Status = channels.StatusManuallyDisabled
	upd.AutoBan = false
	upd.Priority = 50
	if err := s.UpdateChannel(added.ID, upd); err != nil {
		t.Fatal(err)
	}
	v = find()
	if v.Status != channels.StatusManuallyDisabled || v.Priority != 50 || v.AutoBan {
		t.Fatalf("更新未生效：%+v", v)
	}
	if !v.HasKey {
		t.Fatal("留空密钥不得清空原凭证")
	}
}

// newTestUpstream 构造 OpenAI SSE 上游（status>0 时返回该错误码）。
func newTestUpstream(t *testing.T, status int) (*httptest.Server, *string) {
	t.Helper()
	var lastModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		_, _ = w.Write([]byte(testSSEOK))
	}))
	t.Cleanup(srv.Close)
	return srv, &lastModel
}

// 渠道测试：真实链路（选路 → 适配器 → HTTP）返回耗时与回显；
// 失败无副作用（不触发 auto_ban——测试是诊断行为，不能把渠道"测坏"）。
func TestChatService_TestChannel(t *testing.T) {
	srv, lastModel := newTestUpstream(t, 0)
	s := newChannelService(t, Config{})
	added, err := s.AddChannel(llm.Channel{
		Name: "t", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "m1", APIKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := s.TestChannel(context.Background(), added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Reply != "pong" {
		t.Fatalf("成功测试结果 = %+v", res)
	}
	if res.Model != "m1" || *lastModel != "m1" {
		t.Fatalf("测试模型 = %q / 上游收到 %q", res.Model, *lastModel)
	}

	// 失败上游（HTTP 500）：错误含状态码；渠道状态不被测试改动（无副作用）
	bad, _ := newTestUpstream(t, http.StatusInternalServerError)
	added2, err := s.AddChannel(llm.Channel{
		Name: "bad", Protocol: llm.ProtocolOpenAI, BaseURL: bad.URL, Model: "m1", APIKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	res2, err := s.TestChannel(context.Background(), added2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res2.OK || !strings.Contains(res2.Error, "500") {
		t.Fatalf("失败测试结果 = %+v（应含 HTTP 500）", res2)
	}
	views, _, err := s.Channels()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.ID == added2.ID && v.Status != channels.StatusEnabled {
			t.Fatalf("测试不得改动渠道状态：%+v", v)
		}
	}
}

// 测试走 model_mapping：请求打到上游的必须是映射后的真实模型名（否则"生产可用、测试报错"）。
func TestChatService_TestChannelAppliesModelMapping(t *testing.T) {
	srv, lastModel := newTestUpstream(t, 0)
	s := newChannelService(t, Config{})
	added, err := s.AddChannel(llm.Channel{
		Name: "m", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "downstream", APIKey: "k",
		ModelMapping: map[string]string{"downstream": "real-upstream-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TestChannel(context.Background(), added.ID); err != nil {
		t.Fatal(err)
	}
	if *lastModel != "real-upstream-model" {
		t.Fatalf("上游收到模型 = %q, want real-upstream-model", *lastModel)
	}
}

// 凭证管理端到端（IPC 语义）：列表脱敏 + 禁用 + 启用（自动禁用后的恢复途径）。
func TestChatService_CredentialManagement(t *testing.T) {
	s := newChannelService(t, Config{})
	added, err := s.AddChannel(llm.Channel{
		Name: "multi", Protocol: llm.ProtocolOpenAI, BaseURL: "https://gw/v1", Model: "m1",
		APIKey: "k1\nk2\nk3",
	})
	if err != nil {
		t.Fatal(err)
	}
	creds, err := s.ChannelCredentials(added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 3 || !creds[0].Enabled {
		t.Fatalf("凭证列表 = %+v", creds)
	}
	if strings.Contains(creds[0].Preview, "k1") && creds[0].Preview != "•" {
		t.Fatalf("凭证预览不得含明文：%q", creds[0].Preview)
	}

	if err := s.SetCredentialEnabled(added.ID, 0, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCredentialEnabled(added.ID, 0, true); err != nil {
		t.Fatal(err)
	}
	creds, _ = s.ChannelCredentials(added.ID)
	if !creds[0].Enabled {
		t.Fatal("启用后第一条应恢复可用")
	}
}

// 计数上游（备用断言）：测试请求只发一次（无重试放大）。
func TestChatService_TestChannelSingleRequest(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(testSSEOK))
	}))
	t.Cleanup(srv.Close)
	s := newChannelService(t, Config{})
	added, err := s.AddChannel(llm.Channel{
		Name: "once", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "m1", APIKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TestChannel(context.Background(), added.ID); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("测试请求次数 = %d, want 1（诊断请求不放大）", calls)
	}
}

// 渠道级鉴权端到端（0.2.20）：五种形态打到真实上游，断言实际发出/收到的头与 URL。
// 这是"支持多种 auth"的核心契约：Bearer / api-key 头 / 无前缀 Authorization /
// URL 参数 / 无鉴权——覆盖大量中转与自建网关的非标准鉴权。
func TestChatService_ChannelAuthModes(t *testing.T) {
	cases := []struct {
		name   string
		auth   *llm.AuthConfig
		assert func(t *testing.T, h http.Header, rawQuery string)
	}{
		{"默认 Bearer（协议默认不变）", nil, func(t *testing.T, h http.Header, _ string) {
			if got := h.Get("Authorization"); got != "Bearer k1" {
				t.Fatalf("Authorization = %q", got)
			}
		}},
		{"api-key 头（Azure 风格）", &llm.AuthConfig{Type: llm.AuthHeader, Name: "api-key", Value: "{api_key}"}, func(t *testing.T, h http.Header, _ string) {
			if got := h.Get("api-key"); got != "k1" {
				t.Fatalf("api-key = %q", got)
			}
			if h.Get("Authorization") != "" {
				t.Fatal("覆盖后不得残留协议默认 Authorization（否则上游可能取错凭证）")
			}
		}},
		{"无 Bearer 前缀的 Authorization", &llm.AuthConfig{Type: llm.AuthHeader, Name: "Authorization", Value: "{api_key}"}, func(t *testing.T, h http.Header, _ string) {
			if got := h.Get("Authorization"); got != "k1" {
				t.Fatalf("Authorization = %q, want 裸凭证", got)
			}
		}},
		{"URL 查询参数（Vertex/Gemini 风格）", &llm.AuthConfig{Type: llm.AuthQuery, Name: "key", Value: "{api_key}"}, func(t *testing.T, h http.Header, rawQuery string) {
			if h.Get("Authorization") != "" {
				t.Fatal("query 型不得设鉴权头")
			}
			if !strings.Contains(rawQuery, "key=k1") {
				t.Fatalf("rawQuery = %q, want 含 key=k1", rawQuery)
			}
		}},
		{"无鉴权（前置代理已鉴权）", &llm.AuthConfig{Type: llm.AuthNone}, func(t *testing.T, h http.Header, _ string) {
			if h.Get("Authorization") != "" || h.Get("api-key") != "" {
				t.Fatal("none 不得携带任何鉴权头")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotHdr http.Header
			var gotQuery string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotHdr = r.Header.Clone()
				gotQuery = r.URL.RawQuery
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(testSSEOK))
			}))
			t.Cleanup(srv.Close)

			s := newChannelService(t, Config{})
			added, err := s.AddChannel(llm.Channel{
				Name: "auth", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "m1", APIKey: "k1",
				Auth: tc.auth,
			})
			if err != nil {
				t.Fatal(err)
			}
			res, err := s.TestChannel(context.Background(), added.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !res.OK {
				t.Fatalf("测试应通过（上游为正常 SSE）：%+v", res)
			}
			tc.assert(t, gotHdr, gotQuery)
		})
	}
}

// 鉴权配置校验：缺名称的 header/query 在保存期拒绝（fail-fast，不留"首次请求才炸"）。
func TestChatService_ChannelAuthValidation(t *testing.T) {
	s := newChannelService(t, Config{})
	base := llm.Channel{Name: "v", Protocol: llm.ProtocolOpenAI, BaseURL: "https://gw/v1", Model: "m1"}
	for _, bad := range []*llm.AuthConfig{
		{Type: llm.AuthHeader},
		{Type: llm.AuthQuery},
		{Type: "magic"},
	} {
		in := base
		in.Auth = bad
		if _, err := s.AddChannel(in); err == nil {
			t.Fatalf("非法鉴权配置必须拒绝：%+v", bad)
		}
	}
	// 合法形态放行
	in := base
	in.Auth = &llm.AuthConfig{Type: llm.AuthHeader, Name: "api-key", Value: "{api_key}"}
	if _, err := s.AddChannel(in); err != nil {
		t.Fatalf("合法鉴权配置应放行：%v", err)
	}
}

// 头覆写 {api_key} 插值端到端：把任意头变成鉴权头（new-api 同款逃生门）。
func TestChatService_HeaderOverrideRendersAPIKey(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(testSSEOK))
	}))
	t.Cleanup(srv.Close)
	s := newChannelService(t, Config{})
	added, err := s.AddChannel(llm.Channel{
		Name: "h", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "m1", APIKey: "k7",
		HeaderOverride: map[string]string{"X-Auth": "Token {api_key}", "X-Static": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TestChannel(context.Background(), added.ID); err != nil {
		t.Fatal(err)
	}
	if got.Get("X-Auth") != "Token k7" || got.Get("X-Static") != "1" {
		t.Fatalf("头覆写插值失败：%q / %q", got.Get("X-Auth"), got.Get("X-Static"))
	}
}

// 模型选择器（0.2.28）：SetActiveModel 激活对应渠道并指定模型；
// 视图回传实际激活的模型（此前前端永远显示 models[0]，切换后显示与运行不一致）；
// 不在渠道模型列表里的模型显式拒绝。
func TestChatService_SetActiveModel(t *testing.T) {
	srv, _ := newTestUpstream(t, 0)
	s := newChannelService(t, Config{})
	a, err := s.AddChannel(llm.Channel{
		Name: "a", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL,
		Model: "m1", Models: []string{"m1", "m2"}, APIKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddChannel(llm.Channel{
		Name: "b", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL,
		Model: "m9", Models: []string{"m9"}, APIKey: "k",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 首渠道自动激活 m1：视图的 Model = 实际激活模型
	activeModelOf := func() (string, string) {
		views, activeID, err := s.Channels()
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range views {
			if v.ID == activeID {
				return activeID, v.Model
			}
		}
		t.Fatal("无激活渠道")
		return "", ""
	}
	if id, m := activeModelOf(); id != a.ID || m != "m1" {
		t.Fatalf("初始 = %s/%s, want a/m1", id, m)
	}

	// 同渠道换模型
	if err := s.SetActiveModel(a.ID, "m2"); err != nil {
		t.Fatal(err)
	}
	if id, m := activeModelOf(); id != a.ID || m != "m2" {
		t.Fatalf("同渠道换模型 = %s/%s, want a/m2", id, m)
	}

	// 跨渠道换模型：激活渠道一并切换（网关按激活渠道选路）
	if err := s.SetActiveModel(b.ID, "m9"); err != nil {
		t.Fatal(err)
	}
	if id, m := activeModelOf(); id != b.ID || m != "m9" {
		t.Fatalf("跨渠道换模型 = %s/%s, want b/m9", id, m)
	}

	// 非法：模型不在列表 / 渠道不存在
	if err := s.SetActiveModel(a.ID, "nope"); err == nil {
		t.Fatal("不在模型列表的模型必须拒绝")
	}
	if err := s.SetActiveModel("no-such", "m1"); err == nil {
		t.Fatal("不存在的渠道必须拒绝")
	}
}

// 0.0.44（C-APP-9）：SendWithModel 的模型校验——必须在某条已配置渠道的模型列表里；
// 非法模型拒绝且不产生任何回合。
func TestChatService_SendWithModelValidation(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	if _, err := s.AddChannel(llm.Channel{
		Name: "a", Protocol: llm.ProtocolOpenAI, BaseURL: "http://127.0.0.1:1", Model: "m1", APIKey: "k", Models: []string{"m1", "m2"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.validateModel("m2"); err != nil {
		t.Fatalf("已知模型应通过：%v", err)
	}
	if err := s.validateModel("nope"); err == nil {
		t.Fatal("未知模型应拒绝")
	}
	if err := s.validateModel("  "); err == nil {
		t.Fatal("空模型应拒绝")
	}
}
