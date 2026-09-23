package providerfactory

import (
	"strings"
	"testing"

	"tiancode/internal/core/llm"
)

// openai 协议 → 构造出可用 provider（Factory 模式，ADR-0005）。
func TestFactory_OpenAIProtocolBuildsProvider(t *testing.T) {
	f := New()
	p, err := f.NewProvider(llm.Channel{
		ID: "c1", Name: "主", Protocol: llm.ProtocolOpenAI,
		BaseURL: "https://gw/v1", Model: "m", APIKey: "sk-x",
	})
	if err != nil {
		t.Fatalf("openai channel must build: %v", err)
	}
	if p == nil {
		t.Fatal("provider must not be nil")
	}
}

// 未实现协议必须**显式报错**（含协议名），绝不静默降级到 openai——
// 静默降级会让用户以为换了供应商其实没换（C-CH 协议纪律）。
func TestFactory_UnsupportedProtocolExplicitError(t *testing.T) {
	f := New()
	for _, proto := range []llm.Protocol{"anthropic", "ollama"} {
		_, err := f.NewProvider(llm.Channel{
			ID: "c1", Name: "x", Protocol: proto, BaseURL: "https://gw", Model: "m", APIKey: "k",
		})
		if err == nil {
			t.Fatalf("protocol %q must be rejected", proto)
		}
		if !strings.Contains(err.Error(), string(proto)) {
			t.Fatalf("protocol %q: err = %v, want mentions protocol name", proto, err)
		}
	}
}
