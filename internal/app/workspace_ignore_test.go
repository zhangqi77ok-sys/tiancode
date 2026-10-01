package app

import "testing"

// 编排层转出契约：壳层 @ 引用经 WorkspaceIgnoredDir 使用与 search 工具同一份
// 定稿清单（单一来源 internal/platform/workspace）——两处绝不能再各自硬编码。
func TestWorkspaceIgnoredDir_SingleSource(t *testing.T) {
	for _, name := range []string{".git", ".idea", ".vscode", "bin", "build", "dist", "node_modules", "obj", "vendor"} {
		if !WorkspaceIgnoredDir(name) {
			t.Fatalf("WorkspaceIgnoredDir(%q) = false，定稿清单缺项", name)
		}
	}
	if WorkspaceIgnoredDir("src") {
		t.Fatal("普通目录不得被忽略")
	}
}
