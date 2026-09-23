package tools

import (
	"context"
	"encoding/json"
	"testing"
)

// stubTool 是注册表测试用的最小工具实现。
type stubTool struct{ name string }

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "stub " + s.name }
func (s *stubTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (s *stubTool) Execute(_ context.Context, _ json.RawMessage) (ToolResult, error) {
	return ToolResult{Content: "ok"}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubTool{name: "fs"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, ok := r.Get("fs")
	if !ok || got.Name() != "fs" {
		t.Fatalf("Get(fs) = %v, %v", got, ok)
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("Get(nope) must miss")
	}
}

// 重名注册必须报错：工具名是模型可见的唯一标识，重名会导致调用歧义。
func TestRegistry_DuplicateRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubTool{name: "fs"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(&stubTool{name: "fs"}); err == nil {
		t.Fatal("duplicate register must fail")
	}
}

// Definitions 输出全部工具定义（供 ChatRequest.Tools 使用），按名称排序保证稳定。
func TestRegistry_DefinitionsSorted(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"shell", "fs"} {
		if err := r.Register(&stubTool{name: n}); err != nil {
			t.Fatal(err)
		}
	}
	defs := r.Definitions()
	if len(defs) != 2 {
		t.Fatalf("definitions = %d, want 2", len(defs))
	}
	if defs[0].Name != "fs" || defs[1].Name != "shell" {
		t.Fatalf("definitions order = [%s, %s], want sorted", defs[0].Name, defs[1].Name)
	}
	if defs[0].Description != "stub fs" {
		t.Fatalf("description = %q", defs[0].Description)
	}
}
