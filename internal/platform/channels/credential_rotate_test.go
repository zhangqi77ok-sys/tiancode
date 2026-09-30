package channels

import (
	"path/filepath"
	"testing"
)

// 第 4 批：多凭证轮询——连续 Select 必须换下一条凭证。
// 这是"网关重试不死打同一条 Key"的基础机制：gateway.forward 的每次尝试都会
// 重新 Select（见 internal/platform/gateway），轮询游标随之推进。
func TestPool_SelectRotatesCredential(t *testing.T) {
	p := NewPool(filepath.Join(t.TempDir(), "channels.json"))
	if err := p.Save(Channel{
		ID: "ch-1", Type: "openai", Name: "c", BaseURL: "https://gw/v1",
		Credential: "key-a\nkey-b", Models: []string{"m"}, Groups: []string{DefaultGroup},
		Status: StatusEnabled, Priority: 100,
	}); err != nil {
		t.Fatal(err)
	}
	s1, err := p.Select(Selection{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := p.Select(Selection{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if s1.Credential == s2.Credential {
		t.Fatalf("连续选取必须轮换凭证（重试换 Key 的基础）：%q / %q", s1.Credential, s2.Credential)
	}
	if s1.ChannelID != s2.ChannelID {
		t.Fatalf("轮换应发生在同一渠道内：%q / %q", s1.ChannelID, s2.ChannelID)
	}
}
