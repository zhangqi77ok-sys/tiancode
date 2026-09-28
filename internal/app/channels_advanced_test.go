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
