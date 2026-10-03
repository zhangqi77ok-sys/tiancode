// .gitignore 顶层目录解析（0.0.26）：只收非 glob 的顶层目录条目，保守不误伤。
package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGitignore(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestParseGitignoreDirs_TopLevelOnly(t *testing.T) {
	root := writeGitignore(t, `
# 注释行
out/
target
dist2
!keep.me
*.log
build/asset
vendor-ish/
`)
	got := GitignoreDirs(root)
	want := map[string]bool{"out": true, "target": true, "dist2": true, "vendor-ish": true}
	if len(got) != len(want) {
		t.Fatalf("解析结果 = %v, want %v", got, want)
	}
	for _, k := range []string{"out", "target", "dist2", "vendor-ish"} {
		if !ExtraIgnoredDir(root, k) {
			t.Fatalf("%s 应被忽略（got %v）", k, got)
		}
	}
	// 保守不误伤：注释、否定、glob、含中间斜杠的路径式条目一律不生效
	for _, k := range []string{"keep.me", "*.log", "build", "asset", "a.log"} {
		if ExtraIgnoredDir(root, k) {
			t.Fatalf("%s 不应被忽略（越界匹配了）", k)
		}
	}
	// 大小写不敏感（与内置清单同口径）
	if !ExtraIgnoredDir(root, "OUT") {
		t.Fatal("OUT 应被忽略（大小写口径不一致）")
	}
}

func TestParseGitignoreDirs_MissingFileIsEmpty(t *testing.T) {
	root := t.TempDir() // 无 .gitignore
	if got := GitignoreDirs(root); len(got) != 0 {
		t.Fatalf("无 .gitignore 时应为空集：%v", got)
	}
	if ExtraIgnoredDir(root, "out") {
		t.Fatal("无 .gitignore 时不得忽略任何目录")
	}
}
