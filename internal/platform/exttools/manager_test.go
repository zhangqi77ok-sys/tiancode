package exttools

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/tools"
	"tiancode/internal/platform/catalog"
)

// newManageTest 构造带临时清单的自管理工具，并记录 onSaved 调用次数。
func newManageTest(t *testing.T) (*ManageTool, *catalog.Store, *int) {
	t.Helper()
	store := catalog.New(filepath.Join(t.TempDir(), "extensions.json"))
	saves := 0
	p := NewManage(store, nil, func() { saves++ })
	return p, store, &saves
}

func callManage(t *testing.T, p *ManageTool, args map[string]any) tools.ToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute 机制错误：%v", err)
	}
	return res
}

// 用户说"帮我把 X 加成 MCP"：add 后必须落盘、默认启用，且结果带验证到的工具清单。
func TestManage_McpAddPersistsAndProbes(t *testing.T) {
	p, store, saves := newManageTest(t)
	p.probeServer = func(_ context.Context, _ catalog.Server) ([]string, error) {
		return []string{"read_file", "list_dir"}, nil
	}
	res := callManage(t, p, map[string]any{
		"action": "mcp_add", "name": "fs",
		"command": "npx", "args": "-y @modelcontextprotocol/server-filesystem C:/tmp",
	})
	if res.IsError {
		t.Fatalf("add 不应失败：%s", res.Content)
	}
	if !strings.Contains(res.Content, "read_file") || !strings.Contains(res.Content, "list_dir") {
		t.Fatalf("结果应带回验证到的工具：%s", res.Content)
	}
	if *saves != 1 {
		t.Fatalf("落盘钩子应触发 1 次，实际 %d", *saves)
	}
	f, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.MCP) != 1 || f.MCP[0].Name != "fs" || !f.MCP[0].Enabled {
		t.Fatalf("清单未按预期持久化：%+v", f.MCP)
	}
}

// 验证失败不回滚：配置已保存，错误原文可见（由模型向用户解释）。
func TestManage_McpAddKeepsConfigWhenProbeFails(t *testing.T) {
	p, store, _ := newManageTest(t)
	p.probeServer = func(_ context.Context, _ catalog.Server) ([]string, error) {
		return nil, errors.New("dial: 找不到命令")
	}
	res := callManage(t, p, map[string]any{
		"action": "mcp_add", "name": "broken", "command": "nope.exe",
	})
	if res.IsError {
		t.Fatalf("验证失败不算 add 失败（配置已保存）：%s", res.Content)
	}
	if !strings.Contains(res.Content, "连接验证失败") || !strings.Contains(res.Content, "找不到命令") {
		t.Fatalf("结果应包含验证失败的原文：%s", res.Content)
	}
	f, _ := store.Load()
	if len(f.MCP) != 1 {
		t.Fatal("配置应已保存")
	}
}

// 重名与缺参是模型最常见的两种手误，必须给出可执行的报错。
func TestManage_McpAddRejectsDuplicateAndMissing(t *testing.T) {
	p, _, _ := newManageTest(t)
	callManage(t, p, map[string]any{"action": "mcp_add", "name": "fs", "command": "npx"})

	res := callManage(t, p, map[string]any{"action": "mcp_add", "name": "fs", "command": "npx"})
	if !res.IsError || !strings.Contains(res.Content, "已存在") {
		t.Fatalf("重名应拒绝并提示：%s", res.Content)
	}
	res = callManage(t, p, map[string]any{"action": "mcp_add", "name": "no-cmd"})
	if !res.IsError || !strings.Contains(res.Content, "command") {
		t.Fatalf("缺 command/url 应拒绝并提示：%s", res.Content)
	}
}

func TestManage_McpRemoveAndUnknown(t *testing.T) {
	p, store, saves := newManageTest(t)
	callManage(t, p, map[string]any{"action": "mcp_add", "name": "fs", "command": "npx"})
	*saves = 0

	res := callManage(t, p, map[string]any{"action": "mcp_remove", "name": "FS"}) // 大小写不敏感
	if res.IsError {
		t.Fatalf("remove 不应失败：%s", res.Content)
	}
	f, _ := store.Load()
	if len(f.MCP) != 0 {
		t.Fatalf("删除后清单应为空：%+v", f.MCP)
	}
	if *saves != 1 {
		t.Fatalf("删除也应触发落盘钩子，实际 %d", *saves)
	}

	res = callManage(t, p, map[string]any{"action": "mcp_remove", "name": "nope"})
	if !res.IsError || !strings.Contains(res.Content, "没有名为") {
		t.Fatalf("删除不存在的应报错：%s", res.Content)
	}
}

func TestManage_SkillAddRemoveRoundtrip(t *testing.T) {
	p, store, _ := newManageTest(t)
	res := callManage(t, p, map[string]any{
		"action": "skill_add", "name": "code-review",
		"description": "审查改动", "body": "先看行为与回归，再看正确性。",
	})
	if res.IsError {
		t.Fatalf("skill_add 不应失败：%s", res.Content)
	}
	f, _ := store.Load()
	if len(f.Skills) != 1 || f.Skills[0].Name != "code-review" || !f.Skills[0].Enabled {
		t.Fatalf("技能未按预期持久化：%+v", f.Skills)
	}

	res = callManage(t, p, map[string]any{"action": "skill_add", "name": "code-review", "body": "x"})
	if !res.IsError || !strings.Contains(res.Content, "已存在") {
		t.Fatalf("重名应拒绝：%s", res.Content)
	}
	res = callManage(t, p, map[string]any{"action": "skill_add", "name": "empty"})
	if !res.IsError || !strings.Contains(res.Content, "body") {
		t.Fatalf("既无 body 也无 description 应拒绝：%s", res.Content)
	}

	res = callManage(t, p, map[string]any{"action": "skill_remove", "name": "code-review"})
	if res.IsError {
		t.Fatalf("remove 不应失败：%s", res.Content)
	}
	f, _ = store.Load()
	if len(f.Skills) != 0 {
		t.Fatalf("删除后清单应为空：%+v", f.Skills)
	}
}

// 模型能看到这个工具的前提是它在注册表里；名字与描述是模型选择它的唯一线索。
func TestManage_NameAndSchema(t *testing.T) {
	p, _, _ := newManageTest(t)
	if p.Name() != "ext_manage" {
		t.Fatalf("name = %q", p.Name())
	}
	var schema map[string]any
	if err := json.Unmarshal(p.Schema(), &schema); err != nil {
		t.Fatalf("schema 不是合法 JSON：%v", err)
	}
	if !strings.Contains(p.Description(), "mcp_add") || !strings.Contains(p.Description(), "skill_add") {
		t.Fatalf("描述应列出关键 action：%s", p.Description())
	}
}

// 前言要让模型知道"用户让加就调 ext_manage"。
func TestPrefaceMentionsSelfManage(t *testing.T) {
	f := catalog.File{Skills: []catalog.Skill{{Name: "debug", Enabled: true}}}
	text := Preface(f)
	if !strings.Contains(text, "ext_manage") {
		t.Fatalf("前言应提示可用 ext_manage 自助增删：%s", text)
	}
}
