package llm

import (
	"strings"
	"testing"
)

// C-CH-2（校验部分）：渠道核心字段缺失必须报错，且错误信息指明字段（供 UI 定位）。
func TestValidateChannel_RequiresCoreFields(t *testing.T) {
	valid := Channel{
		ID: "c1", Name: "主渠道", Protocol: ProtocolOpenAI,
		BaseURL: "https://gw/v1", Model: "m1", APIKey: "sk-x",
	}
	if err := ValidateChannel(valid); err != nil {
		t.Fatalf("valid channel must pass: %v", err)
	}

	cases := []struct {
		name    string
		mutate  func(Channel) Channel
		wantErr string
	}{
		{"missing name", func(c Channel) Channel { c.Name = ""; return c }, "name"},
		{"missing baseUrl", func(c Channel) Channel { c.BaseURL = ""; return c }, "baseUrl"},
		{"missing model", func(c Channel) Channel { c.Model = ""; return c }, "model"},
		{"unknown protocol", func(c Channel) Channel { c.Protocol = Protocol("gemini"); return c }, "protocol"},
	}
	for _, tc := range cases {
		err := ValidateChannel(tc.mutate(valid))
		if err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: err = %v, want mentions %q", tc.name, err, tc.wantErr)
		}
	}
}

// Protocol.Valid：只认已实现的协议，未知协议必须显式无效（防静默降级）。
func TestProtocol_Valid(t *testing.T) {
	if !ProtocolOpenAI.Valid() {
		t.Fatal("openai must be valid")
	}
	for _, p := range []Protocol{"", "gemini", "openai-compatible"} {
		if p.Valid() {
			t.Fatalf("protocol %q must be invalid", p)
		}
	}
}

// Channel.Sanitized：外发视图必须脱敏密钥（C-CH-6），且能告知"是否已配置密钥"。
// 为什么用独立视图类型而不是给 Channel 加 HasKey 字段：持有真密钥的领域对象
// 不应带上"是否已配置"这种展示语义（否则调用方可能误把视图当实体回写）。
func TestChannel_SanitizedHidesKey(t *testing.T) {
	ch := Channel{ID: "c1", Name: "n", Protocol: ProtocolOpenAI, BaseURL: "u", Model: "m", APIKey: "sk-secret"}
	var view ChannelView = ch.Sanitized()
	if view.APIKey != "" {
		t.Fatalf("sanitized APIKey = %q, want empty", view.APIKey)
	}
	if !view.HasKey {
		t.Fatal("sanitized must report HasKey=true")
	}
	if view.ID != ch.ID || view.Model != ch.Model {
		t.Fatalf("view must keep non-secret fields: %+v", view)
	}
	if noKey := (Channel{ID: "c2"}).Sanitized(); noKey.HasKey {
		t.Fatal("HasKey must be false when no key configured")
	}
}
