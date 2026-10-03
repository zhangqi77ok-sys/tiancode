// search 端到端：.gitignore 的顶层条目必须真的被跳过（0.0.26）。
package searchtool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearch_SkipsGitignoreTopLevelDirs(t *testing.T) {
	tool := newWS(t)
	// 用户自定义输出目录（此前会淹没搜索结果）
	write(t, tool.root, "out/generated.log", "HIT-out")
	write(t, tool.root, "target/debug/x.txt", "HIT-target")
	write(t, tool.root, "src/keep.txt", "HIT-src")
	if err := os.WriteFile(filepath.Join(tool.root, ".gitignore"), []byte("out/\ntarget\n# 注释\n*.tmp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"pattern": "HIT-"}))
	if err != nil || res.IsError {
		t.Fatalf("%v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "HIT-src") {
		t.Fatalf("正常目录应被搜到：%s", res.Content)
	}
	if strings.Contains(res.Content, "HIT-out") || strings.Contains(res.Content, "HIT-target") {
		t.Fatalf(".gitignore 里的目录未被跳过：%s", res.Content)
	}
}
