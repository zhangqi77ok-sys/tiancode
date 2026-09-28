package exttools

import (
	"strings"
	"testing"

	"tiancode/internal/platform/catalog"
)

func TestPreface_OptionalNotMandatory(t *testing.T) {
	text := Preface(catalog.File{
		Skills: []catalog.Skill{{Name: "code-review", Description: "审查改动", Enabled: true}},
		MCP:    []catalog.Server{{Name: "filesystem", Command: "npx.cmd", Args: "-y @modelcontextprotocol/server-filesystem", Enabled: true}},
	})
	if text == "" {
		t.Fatal("empty preface")
	}
	for _, want := range []string{"不是每轮都必须调用", "不要因为它们出现在下面就去调用", "code-review", "filesystem", "tool 设为 list"} {
		if !strings.Contains(text, want) {
			t.Fatalf("preface missing %q\n%s", want, text)
		}
	}
}

func TestPreface_EmptyWhenNothingEnabled(t *testing.T) {
	if got := Preface(catalog.File{
		Skills: []catalog.Skill{{Name: "x", Enabled: false}},
		MCP:    []catalog.Server{{Name: "y", Enabled: false}},
	}); got != "" {
		t.Fatalf("got %q", got)
	}
}
