package workspace

import "testing"

// 定稿清单契约：searchtool 与壳层 @ 引用两处硬编码的并集去重后恰好 9 项——
// 缺一项就有一处忽略目录回归硬编码，多一项就是清单悄悄膨胀。
func TestIgnoredDir_FinalList(t *testing.T) {
	final := []string{".git", ".idea", ".vscode", "bin", "build", "dist", "node_modules", "obj", "vendor"}
	if len(defaultIgnoreDirs) != len(final) {
		t.Fatalf("定稿清单应为 %d 项，实为 %d 项：%v", len(final), len(defaultIgnoreDirs), defaultIgnoreDirs)
	}
	for _, name := range final {
		if !defaultIgnoreDirs[name] {
			t.Fatalf("定稿清单缺 %q", name)
		}
		if !IgnoredDir(name) {
			t.Fatalf("IgnoredDir(%q) = false，应为 true", name)
		}
	}
}

// 大小写不敏感：Windows/macOS 文件系统大小写不敏感，.GIT 就是 .git
// （search 原本即此规则，@ 引用从精确匹配收紧到同一规则只会少列噪音）。
func TestIgnoredDir_CaseInsensitive(t *testing.T) {
	for _, name := range []string{".GIT", "Node_Modules", "BUILD", "Vendor", "Obj", ".VSCode"} {
		if !IgnoredDir(name) {
			t.Fatalf("IgnoredDir(%q) = false，应为 true（大小写不敏感）", name)
		}
	}
}

// 整名匹配：清单不做前缀/子串匹配，相邻名字的目录不得误伤。
func TestIgnoredDir_NoPrefixMatch(t *testing.T) {
	for _, name := range []string{"src", "internal", "cmd", "docs", ".github", "binaries", "build2", "objx", "vendors", "distribution", "git", "idea"} {
		if IgnoredDir(name) {
			t.Fatalf("IgnoredDir(%q) = true，应整名匹配不误伤", name)
		}
	}
}
