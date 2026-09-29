package fstool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 0.0.06：tree = 有界目录骨架——深度最多 2 层、条目总数 500 封顶、越界拒绝。
// 用途边界：先 tree 了解项目结构，再 list 看某目录确切内容。
func TestFSTree_BoundedSkeleton(t *testing.T) {
	tool := newTool(t)
	// 结构：a/b/c（3 层）+ 顶层文件 top.txt
	mustWrite(t, tool, "top.txt", "x")
	mustWrite(t, tool, "a/leaf1.txt", "x")
	mustWrite(t, tool, "a/b/leaf2.txt", "x")
	mustWrite(t, tool, "a/b/c/leaf3.txt", "x")

	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "tree", "path": ""}))
	if err != nil || res.IsError {
		t.Fatalf("tree: %v %s", err, res.Content)
	}
	out := res.Content
	// 第 1 层
	if !strings.Contains(out, "a/") || !strings.Contains(out, "top.txt") {
		t.Fatalf("第 1 层缺失：%s", out)
	}
	// 第 2 层（a 的子项）
	if !strings.Contains(out, "b/") || !strings.Contains(out, "leaf1.txt") {
		t.Fatalf("第 2 层缺失：%s", out)
	}
	// 第 3 层绝不出现（深度封顶）：c/ 与 leaf2/leaf3 都在第 3 层及以下
	if strings.Contains(out, "c/") || strings.Contains(out, "leaf2.txt") || strings.Contains(out, "leaf3.txt") {
		t.Fatalf("超出深度 2 的条目出现：%s", out)
	}
}

func TestFSTree_EntryLimitAndEscape(t *testing.T) {
	tool := newTool(t)
	// 520 个顶层文件 → 500 封顶 + truncated 标注
	for i := 0; i < 520; i++ {
		mustWrite(t, tool, fmt.Sprintf("f%03d.txt", i), "x")
	}
	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "tree", "path": ""}))
	if err != nil || res.IsError {
		t.Fatalf("tree: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "truncated") {
		t.Fatalf("超限必须标注：%s", res.Content[len(res.Content)-120:])
	}
	if got := strings.Count(res.Content, ".txt"); got != treeLimit {
		t.Fatalf("条目数 = %d, want %d（有界）", got, treeLimit)
	}

	// 越界拒绝（与 read/list 同一守卫）
	for _, p := range []string{"../outside", "a/../../x"} {
		res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "tree", "path": p}))
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError || !strings.Contains(res.Content, "escapes workspace") {
			t.Fatalf("越界路径必须拒绝：%q → %q", p, res.Content)
		}
	}

	// 文件路径不是目录 → 明确报错
	res, err = tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "tree", "path": "f000.txt"}))
	if err != nil || !res.IsError || !strings.Contains(res.Content, "not a directory") {
		t.Fatalf("文件路径应报 not a directory：%v %q", err, res.Content)
	}
}

// 子目录起点：tree 从指定目录向下（相对骨架），路径拼接正确。
func TestFSTree_SubdirectoryStart(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "src/pkg/deep.txt", "x")
	mustWrite(t, tool, "src/pkg/sub/deeper.txt", "x")
	mustWrite(t, tool, "src/main.go", "x")
	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "tree", "path": "src"}))
	if err != nil || res.IsError {
		t.Fatalf("tree: %v %s", err, res.Content)
	}
	// 第 1 层：main.go、pkg/；第 2 层：pkg 的子项 deep.txt（sub/ 同级）
	if !strings.Contains(res.Content, "pkg/") || !strings.Contains(res.Content, "main.go") || !strings.Contains(res.Content, "deep.txt") {
		t.Fatalf("子目录骨架缺失：%s", res.Content)
	}
	// 第 3 层绝不出现：deeper.txt 在第 3 层（sub/ 本身是合法的第 2 层条目）
	if strings.Contains(res.Content, "deeper.txt") {
		t.Fatalf("超出深度 2 的条目出现：%s", res.Content)
	}
	_ = os.Remove(filepath.Join(tool.Root(), "src", "main.go")) // 防误用告警
}
