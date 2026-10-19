package searchtool

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// 2026-10-19：git 仓库里按 git 自己的规则过滤——子目录 .gitignore 与文件级规则都生效
// （此前只认根 .gitignore 的顶层目录条目）；未跟踪但未忽略的新文件照常可搜到。
func TestSearch_RespectsNestedGitignore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tool := newWS(t)
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = tool.root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	write(t, tool.root, "src/a.go", "needle in source\n")
	write(t, tool.root, "src/.gitignore", "*.log\ngen/\n")
	write(t, tool.root, "src/debug.log", "needle in log\n")
	write(t, tool.root, "src/gen/out.go", "needle in generated\n")
	write(t, tool.root, "notes.txt", "needle untracked but not ignored\n")

	for _, filesOnly := range []bool{false, true} {
		pattern := "needle"
		if filesOnly {
			pattern = "."
		}
		res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": pattern, "files_only": filesOnly}))
		if err != nil || res.IsError {
			t.Fatalf("search failed: %v %s", err, res.Content)
		}
		if !strings.Contains(res.Content, "src/a.go") || !strings.Contains(res.Content, "notes.txt") {
			t.Fatalf("filesOnly=%v：应包含源码与未忽略的新文件：%s", filesOnly, res.Content)
		}
		if strings.Contains(res.Content, "debug.log") || strings.Contains(res.Content, "gen/out.go") {
			t.Fatalf("filesOnly=%v：子目录 .gitignore 忽略的文件不应出现：%s", filesOnly, res.Content)
		}
	}
}
