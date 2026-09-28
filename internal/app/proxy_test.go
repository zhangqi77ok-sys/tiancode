package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// 全局代理端到端：设置代理后渠道请求必须经代理发出。
// 证明方式：渠道 base_url 指向不可达地址（127.0.0.1:1）——只有请求真的走了代理
// 才可能成功；代理服务器（httptest）记录命中次数。
// 背景（0.2.21 实机）：授权与推理直连被 OpenAI 按地区拒绝，代理是可用性前提。
func TestChatService_ProxyRoutesRequests(t *testing.T) {
	var hits int32
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexSSE))
	}))
	t.Cleanup(proxySrv.Close)

	s := newChannelService(t, Config{})

	// 非法代理必须显式拒绝（静默按直连处理会让地区封锁类错误极难排查）
	if err := s.SetProxy("not-a-url"); err == nil {
		t.Fatal("无效代理地址必须拒绝")
	}
	if err := s.SetProxy("socks5://127.0.0.1:1080"); err == nil {
		t.Fatal("不支持的代理协议必须拒绝")
	}
	if err := s.SetProxy("http://127.0.0.1:7897"); err != nil {
		t.Fatal(err)
	}
	if got := s.Proxy(); got != "http://127.0.0.1:7897" {
		t.Fatalf("Proxy() = %q", got)
	}
	if err := s.SetProxy(proxySrv.URL); err != nil {
		t.Fatal(err)
	}

	// 建一条 codex 渠道（远期凭证），把 base_url 指向不可达地址
	credJSON := fmt.Sprintf(
		`{"type":"codex_oauth","access_token":"at-1","refresh_token":"rt-1","expires_at":%d,"email":"u@e.com"}`,
		time.Now().Add(24*time.Hour).Unix())
	res, err := s.ImportCodexCredential(context.Background(), "", "", credJSON)
	if err != nil {
		t.Fatal(err)
	}
	upd := llm.Channel{
		ID: res.ChannelID, Name: res.Name, Protocol: llm.ProtocolCodex,
		BaseURL: "http://127.0.0.1:1", Models: []string{DefaultCodexModel},
	}
	if err := s.UpdateChannel(res.ChannelID, upd); err != nil {
		t.Fatal(err)
	}

	tr, err := s.TestChannel(context.Background(), res.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.OK || tr.Reply != "pong" {
		t.Fatalf("经代理的测试未通过：%+v", tr)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("请求未经代理（代理服务器零命中）")
	}

	// 清空代理 → 恢复直连：请求应失败（不可达地址）
	if err := s.SetProxy(""); err != nil {
		t.Fatal(err)
	}
	tr2, err := s.TestChannel(context.Background(), res.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if tr2.OK {
		t.Fatalf("直连不可达地址不应成功：%+v", tr2)
	}
}

// 代理配置持久化：重开服务后仍在（与渠道同一文件，启动即生效）。
func TestChatService_ProxyPersists(t *testing.T) {
	dataDir := t.TempDir()
	channelsPath := t.TempDir() + "/channels.json"
	s, err := NewChatService(Config{
		DataDir: dataDir, WorkDir: t.TempDir(), ChannelsPath: channelsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetProxy("http://127.0.0.1:7897"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewChatService(Config{
		DataDir: dataDir, WorkDir: t.TempDir(), ChannelsPath: channelsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })
	if got := s2.Proxy(); got != "http://127.0.0.1:7897" {
		t.Fatalf("重启后 Proxy() = %q（应持久化）", got)
	}
}
