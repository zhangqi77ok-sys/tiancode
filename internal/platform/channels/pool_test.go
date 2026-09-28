package channels

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tiancode/internal/platform/configfile"
)

// 旧格式（llm.Channel 单渠道）自动迁移为 v2，迁移后即可被 selector 使用。
func TestPool_LegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channels.json")
	legacy := `{"channels":[{"id":"c1","name":"旧渠道","protocol":"openai","baseUrl":"https://x/v1","apiKey":"sk-1","model":"m1"}],"activeId":"c1"}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	p := NewPool(path)
	if err := p.Load(); err != nil {
		t.Fatal(err)
	}
	ch, ok := p.Get("c1")
	if !ok {
		t.Fatal("迁移后渠道丢失")
	}
	if ch.Type != "openai" || ch.Models[0] != "m1" || ch.Groups[0] != DefaultGroup ||
		ch.Priority != 100 || ch.Credential != "sk-1" || ch.Status != StatusEnabled {
		t.Fatalf("迁移字段不符：%+v", ch)
	}
	got, err := p.Select(Selection{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ChannelID != "c1" || got.Credential != "sk-1" || got.CredentialIndex != -1 {
		t.Fatalf("迁移后选路 = %+v", got)
	}
}

// 保存渠道即重建 Ability：禁用后选不到，恢复后可选；RebuildAbility 幂等。
func TestPool_SaveRebuildsAbility(t *testing.T) {
	p := newTestPool(t, testCh("a", "m", 100, 1))
	if _, err := p.Select(Selection{Model: "m"}); err != nil {
		t.Fatalf("启用态应可选：%v", err)
	}
	dis := testCh("a", "m", 100, 1)
	dis.Status = StatusManuallyDisabled
	if err := p.Save(dis); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Select(Selection{Model: "m"}); !errors.Is(err, ErrNoChannel) {
		t.Fatalf("禁用后应不可选：%v", err)
	}
	p.RebuildAbility()
	if _, err := p.Select(Selection{Model: "m"}); !errors.Is(err, ErrNoChannel) {
		t.Fatalf("RebuildAbility 不得复活禁用渠道：%v", err)
	}
	if err := p.Save(testCh("a", "m", 100, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Select(Selection{Model: "m"}); err != nil {
		t.Fatalf("恢复后应可选：%v", err)
	}
}

// C-CH-1：config.json 首启迁移；占位符绝不变成真渠道。
func TestMigrateFromConfig(t *testing.T) {
	ch, ok := MigrateFromConfig(configfile.File{BaseURL: "https://gw/v1", APIKey: "sk-1", Model: "m1"})
	if !ok || ch.BaseURL != "https://gw/v1" || ch.Credential != "sk-1" || ch.Models[0] != "m1" {
		t.Fatalf("迁移 = %+v ok=%v", ch, ok)
	}
	if _, ok := MigrateFromConfig(configfile.File{BaseURL: "https://your-gateway.example/v1", Model: "m"}); ok {
		t.Fatal("占位符渠道不得迁移")
	}
	if _, ok := MigrateFromConfig(configfile.File{BaseURL: "https://gw/v1"}); ok {
		t.Fatal("缺 model 不得迁移")
	}
}