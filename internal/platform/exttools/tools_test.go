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

// mcp 找不到服务器时的报错必须"可继续推理"：列出已启用清单，并把网页诉求指回
// 内置 browser 工具。实证（2026-10-01）：模型找 cursor-ide-browser 碰壁后，拿到
// 干巴巴的"没有"就误判"内置浏览器用不了"，绕道 shell 打开，用户什么都看不到。
func TestMCPNotFoundContent_PointsBackToBrowser(t *testing.T) {
	load := func() catalog.File {
		return catalog.File{MCP: []catalog.Server{
			{Name: "context7", Enabled: true},
			{Name: "memory", Enabled: true},
			{Name: "停用的不算", Enabled: false},
		}}
	}
	got := mcpNotFoundContent(load, "cursor-ide-browser")
	if !strings.Contains(got, "cursor-ide-browser") || !strings.Contains(got, "context7、memory") || strings.Contains(got, "停用的不算") {
		t.Fatalf("必须带服务器名与已启用清单（停用项不入列）：%s", got)
	}
	if !strings.Contains(got, "内置 browser") {
		t.Fatalf("必须把网页诉求指回内置 browser 工具：%s", got)
	}
	got = mcpNotFoundContent(nil, "x")
	if !strings.Contains(got, "（已启用：无）") || !strings.Contains(got, "内置 browser") {
		t.Fatalf("空清单也要有指引：%s", got)
	}
}
