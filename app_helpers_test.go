package main

import (
	"testing"

	"tiancode/internal/core/sandbox"
	"tiancode/internal/host"
	"tiancode/internal/mcp"
)

// TestRegisterWorkspaceTools_RegistersExactlyOnce 锁定 T0 修复：
// NewApp 与 SetWorkspace 共用 registerWorkspaceTools，重复调用不得产生重复条目。
func TestRegisterWorkspaceTools_RegistersExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	reg := host.NewRegistry()
	sb, err := sandbox.NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	sm := sandbox.NewSnapshotManager(dir)

	registerWorkspaceTools(reg, dir, sb, sm)
	if got := len(reg.ListTools()); got != 5 {
		t.Fatalf("首次注册后应有 5 个工具，实际 %d", got)
	}

	// 模拟 SetWorkspace 再次注册（T0 修复前这里会再重复 5 条）
	registerWorkspaceTools(reg, dir, sb, sm)
	if got := len(reg.ListTools()); got != 5 {
		t.Fatalf("重复注册后工具数应仍为 5（RegisterOrReplace 幂等），实际 %d", got)
	}

	// 关键工作区工具必须存在
	for _, id := range []string{"tool.git", "tool.fs", "tool.terminal", "tool.search", "tool.arch"} {
		if _, ok := reg.GetTool(id); !ok {
			t.Errorf("缺少工作区工具 %s", id)
		}
	}
}

// TestConfigureEngine_ReturnsConfiguredEngine 锁定 T0 修复：
// configureEngine 一次性构造并返回已注入 MCP/Verify 的引擎，不再先建后弃。
func TestConfigureEngine_ReturnsConfiguredEngine(t *testing.T) {
	dir := t.TempDir()
	reg := host.NewRegistry()
	mgr := mcp.NewManager(dir)

	eng := configureEngine(reg, nil, mgr, dir)
	if eng == nil {
		t.Fatal("configureEngine 应返回非 nil 引擎")
	}
}
