package channels

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/configfile"
)

// C-CH-1（前半）：文件不存在 → 空配置且不报错（调用方据此判断是否需要迁移）。
func TestStore_LoadMissingReturnsEmpty(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "channels.json"))
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if len(cfg.Channels) != 0 || cfg.ActiveID != "" {
		t.Fatalf("cfg = %+v, want empty", cfg)
	}
}

// C-CH-5：保存走原子写——往返一致、无临时文件残留。
func TestStore_SaveLoadRoundtripAtomic(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "channels.json"))
	want := Config{
		Channels: []llm.Channel{
			{ID: "c1", Name: "主", Protocol: llm.ProtocolOpenAI, BaseURL: "https://gw/v1", Model: "m1", APIKey: "sk-a"},
			{ID: "c2", Name: "备", Protocol: llm.ProtocolOpenAI, BaseURL: "https://gw2/v1", Model: "m2", APIKey: "sk-b"},
		},
		ActiveID: "c2",
	}
	if err := s.Save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("roundtrip mismatch:\n got %+v\nwant %+v", got, want)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp leftover: %s", e.Name())
		}
	}
}

// 损坏 JSON 必须报错并指明路径（用户可定位）。
func TestStore_LoadCorruptMentionsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path).Load(); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want mentions %s", err, path)
	}
}

// C-CH-1（后半）：config.json 有值时迁移为一个渠道并激活；空配置不迁移。
func TestMigrateFromConfig(t *testing.T) {
	src := configfile.File{
		BaseURL: "https://gw/v1", APIKey: "sk-x", Model: "m1", Workspace: "C:\\proj",
	}
	cfg, ok := MigrateFromConfig(src)
	if !ok {
		t.Fatal("non-empty config must produce a migrated channel")
	}
	if len(cfg.Channels) != 1 {
		t.Fatalf("channels = %d, want 1", len(cfg.Channels))
	}
	ch := cfg.Channels[0]
	if ch.BaseURL != src.BaseURL || ch.APIKey != src.APIKey || ch.Model != src.Model {
		t.Fatalf("migrated channel = %+v, want copied from config", ch)
	}
	if ch.Protocol != llm.ProtocolOpenAI || ch.ID == "" || ch.Name == "" {
		t.Fatalf("migrated channel must be complete: %+v", ch)
	}
	if cfg.ActiveID != ch.ID {
		t.Fatalf("migrated channel must be active: %+v", cfg)
	}

	if _, ok := MigrateFromConfig(configfile.File{}); ok {
		t.Fatal("empty config must not migrate")
	}
}

// 模板占位符不得迁移成渠道。
// 实机事故：config.json 里是模板值（your-gateway.example / your-model），
// 首启自动"迁移"出一个看着有渠道、实际连不通的假渠道——比没有渠道更误导。
func TestMigrateFromConfig_RejectsPlaceholders(t *testing.T) {
	cases := []configfile.File{
		{BaseURL: "https://your-gateway.example/v1", APIKey: "sk-real", Model: "your-model"},
		{BaseURL: "https://api.deepseek.com/v1", APIKey: "your-api-key", Model: "deepseek-chat"},
		{BaseURL: "https://placeholder.example/v1", APIKey: "sk-real", Model: "some-model"},
		{BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-real", Model: "change-me"},
	}
	for _, src := range cases {
		if cfg, ok := MigrateFromConfig(src); ok {
			t.Fatalf("placeholder config must not migrate: %+v → %+v", src, cfg)
		}
	}
	// 真实值仍然迁移（别把守卫写得太宽）
	if _, ok := MigrateFromConfig(configfile.File{
		BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-abc123", Model: "deepseek-chat",
	}); !ok {
		t.Fatal("real config must still migrate")
	}
}
