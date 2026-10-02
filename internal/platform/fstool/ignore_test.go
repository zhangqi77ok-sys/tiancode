// 忽略目录过滤测试（0.0.21）：list/tree 与 search / @ 引用同一份清单——
// node_modules / .git 不出现在输出里，同名文件不误杀。
package fstool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListAndTree_FilterIgnoredDirs(t *testing.T) {
	tool := newTool(t)
	root := tool.Root()
	mustWrite(t, tool, "keep.txt", "x")
	mustWrite(t, tool, "node_modules.txt", "同名文件不误杀") // 文件：即便名字沾边也要保留
	for _, d := range []string{"node_modules/a", ".git/objects", ".idea/lib", "src"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// list：忽略目录不出现，同名文件保留
	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "list"}))
	if err != nil || res.IsError {
		t.Fatalf("list: %v %s", err, res.Content)
	}
	content := res.Content
	if strings.Contains(content, "node_modules/") || strings.Contains(content, ".git/") || strings.Contains(content, ".idea/") {
		t.Fatalf("list 输出含忽略目录：%s", content)
	}
	if !strings.Contains(content, "node_modules.txt") {
		t.Fatalf("同名文件被误杀：%s", content)
	}
	if !strings.Contains(content, "src/") || !strings.Contains(content, "keep.txt") {
		t.Fatalf("正常条目丢失：%s", content)
	}

	// tree：不下钻 node_modules（深度预算不被垃圾吃掉）
	res, err = tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "tree"}))
	if err != nil || res.IsError {
		t.Fatalf("tree: %v %s", err, res.Content)
	}
	// 带斜杠匹配（裸 "node_modules" 会被根下的 node_modules.txt 同名文件误命中）
	if strings.Contains(res.Content, "node_modules/") || strings.Contains(res.Content, ".git/") || strings.Contains(res.Content, ".idea/") || strings.Contains(res.Content, "a/") {
		t.Fatalf("tree 输出含忽略目录及其内容：%s", res.Content)
	}
	if !strings.Contains(res.Content, "src/") {
		t.Fatalf("tree 正常条目丢失：%s", res.Content)
	}
}
