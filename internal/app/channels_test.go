package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// newChannelService 构造带独立渠道文件的用例服务（测试隔离）。
func newChannelService(t *testing.T, cfg Config) *ChatService {
	t.Helper()
	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = t.TempDir()
	}
	cfg.ChannelsPath = filepath.Join(t.TempDir(), "channels.json")
	s, err := NewChatService(cfg)
	if err != nil {
		t.Fatalf("NewChatService: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func testChannel(name, baseURL string) llm.Channel {
	return llm.Channel{
		Name: name, Protocol: llm.ProtocolOpenAI,
		BaseURL: baseURL, Model: "m1", APIKey: "sk-" + name,
	}
}

// C-CH-1：config.json 有值时首次启动自动迁移为一个激活渠道（免二次配置）。
func TestChatService_MigratesConfigToFirstChannel(t *testing.T) {
	s := newChannelService(t, Config{
		BaseURL: "https://gw/v1", APIKey: "sk-x", Model: "m1",
	})
	views, activeID, err := s.Channels()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || activeID == "" || views[0].ID != activeID {
		t.Fatalf("views = %+v activeID = %q", views, activeID)
	}
	if views[0].BaseURL != "https://gw/v1" || views[0].Model != "m1" {
		t.Fatalf("migrated view = %+v", views[0])
	}
}

// C-CH-2：CRUD 校验——非法渠道拒绝；删除激活渠道拒绝（必须先切换）。
func TestChatService_ChannelCRUDValidation(t *testing.T) {
	s := newChannelService(t, Config{BaseURL: "https://gw/v1", APIKey: "sk-x", Model: "m1"})
	views, activeID, err := s.Channels()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("seed channels = %d", len(views))
	}

	// 缺字段 → 拒绝
	if _, err := s.AddChannel(llm.Channel{Name: "坏", Protocol: llm.ProtocolOpenAI, BaseURL: "", Model: "m"}); err == nil {
		t.Fatal("invalid channel must be rejected")
	}
	// 未实现协议 → 拒绝（含协议名）
	_, err = s.AddChannel(llm.Channel{Name: "x", Protocol: "anthropic", BaseURL: "https://a", Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "anthropic") {
		t.Fatalf("unsupported protocol must be rejected with name: %v", err)
	}

	// 正常新增 → 出现在列表（脱敏：无明文密钥，但有 HasKey）
	ch, err := s.AddChannel(testChannel("备", "https://gw2/v1"))
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID == "" {
		t.Fatal("added channel must get an ID")
	}
	views, _, err = s.Channels()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("channels = %d, want 2", len(views))
	}
	for _, v := range views {
		if v.APIKey != "" {
			t.Fatalf("view leaks key: %+v", v)
		}
		if !v.HasKey {
			t.Fatalf("view must report HasKey: %+v", v)
		}
	}

	// 删除激活渠道 → 拒绝
	if err := s.DeleteChannel(activeID); err == nil {
		t.Fatal("deleting active channel must be rejected")
	}
	// 删除非激活渠道 → 成功
	if err := s.DeleteChannel(ch.ID); err != nil {
		t.Fatalf("delete inactive: %v", err)
	}
	views, _, _ = s.Channels()
	if len(views) != 1 {
		t.Fatalf("channels after delete = %d, want 1", len(views))
	}
}

// C-CH-3：切换激活渠道后，下一次 Send 必须打到新渠道的上游（runtime 重建生效）。
func TestChatService_SetActiveRebuildsRuntime(t *testing.T) {
	hits := make(chan string, 4)
	newUpstream := func(which string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits <- which
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}))
	}
	upA, upB := newUpstream("A"), newUpstream("B")
	defer upA.Close()
	defer upB.Close()

	s := newChannelService(t, Config{BaseURL: upA.URL, APIKey: "sk", Model: "m1"})
	ctx := context.Background()

	drain := func(ch <-chan llm.StreamChunk) {
		deadline := time.After(5 * time.Second)
		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
			case <-deadline:
				t.Fatal("stream did not close")
			}
		}
	}

	ch1, err := s.Send(ctx, "s1", "hi")
	if err != nil {
		t.Fatal(err)
	}
	drain(ch1)
	if got := <-hits; got != "A" {
		t.Fatalf("first send hit %q, want A", got)
	}

	// 新增指向 B 的渠道并切换为激活
	chB, err := s.AddChannel(testChannel("B", upB.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveChannel(chB.ID); err != nil {
		t.Fatal(err)
	}
	ch2, err := s.Send(ctx, "s2", "hi")
	if err != nil {
		t.Fatal(err)
	}
	drain(ch2)
	if got := <-hits; got != "B" {
		t.Fatalf("after switch hit %q, want B", got)
	}
}

// C-CH-4：模型发现——成功返回排序后的 id 列表；失败报错且不改动已保存配置。
func TestChatService_DiscoverModels(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "z-model"}, {"id": "a-model"}},
		})
	}))
	defer up.Close()

	s := newChannelService(t, Config{BaseURL: up.URL, APIKey: "sk", Model: "m1"})
	models, err := s.DiscoverModels(context.Background(), llm.Channel{
		Protocol: llm.ProtocolOpenAI, BaseURL: up.URL, APIKey: "sk",
	})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(models) != 2 || models[0] != "a-model" || models[1] != "z-model" {
		t.Fatalf("models = %v, want sorted [a-model z-model]", models)
	}

	// 失败路径：上游 500 → 报错；已保存配置不得被改动
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	if _, err := s.DiscoverModels(context.Background(), llm.Channel{Protocol: llm.ProtocolOpenAI, BaseURL: bad.URL, APIKey: "k"}); err == nil {
		t.Fatal("discovery failure must return error")
	}
	views, activeID, err := s.Channels()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || activeID != views[0].ID {
		t.Fatalf("discovery failure must not mutate config: %+v active=%q", views, activeID)
	}
}

// 无可用渠道时 Send 必须给出可读错误（而非静默失败）。
func TestChatService_NoChannelSendFails(t *testing.T) {
	s := newChannelService(t, Config{}) // 无 config 迁移来源
	if _, err := s.Send(context.Background(), "s1", "hi"); err == nil {
		t.Fatal("Send without channel must fail explicitly")
	}
}
