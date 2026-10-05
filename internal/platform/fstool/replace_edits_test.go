// 多段编辑（0.0.34）用例：一次调用多个 hunk 原子应用；任一段失败文件零修改；
// 后面的段在前面的段应用后的内容上匹配；与单段参数互斥。
package fstool

import (
	"context"
	"strings"
	"testing"
)

func editsArgs(path string, edits ...map[string]string) map[string]any {
	es := make([]map[string]string, 0, len(edits))
	for _, e := range edits {
		es = append(es, e)
	}
	return map[string]any{"action": "replace", "path": path, "edits": es}
}

func replaceEdits(t *testing.T, tool *Tool, args map[string]any) (res struct {
	Content string
	IsError bool
},
) {
	t.Helper()
	r, _ := tool.Execute(context.Background(), mustArgs(t, args))
	return struct {
		Content string
		IsError bool
	}{r.Content, r.IsError}
}

func TestReplaceEdits_AppliesAll(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "a.txt", "alpha\nbeta\ngamma\n")
	res := replaceEdits(t, tool, editsArgs("a.txt",
		map[string]string{"target": "alpha", "replacement": "ALPHA"},
		map[string]string{"target": "gamma", "replacement": "GAMMA"},
	))
	if res.IsError {
		t.Fatalf("多段编辑不应失败：%s", res.Content)
	}
	got, err := tool.read("a.txt", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ALPHA", "beta", "GAMMA"} {
		if !strings.Contains(got.Content, want) {
			t.Fatalf("缺 %q：%q", want, got.Content)
		}
	}
	if !strings.Contains(res.Content, "applied 2 edit(s)") {
		t.Fatalf("回执应汇总段数：%q", res.Content)
	}
}

func TestReplaceEdits_SequentialDependency(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "b.txt", "start\n")
	// 第二段的 target 是第一段刚替换出来的文本
	res := replaceEdits(t, tool, editsArgs("b.txt",
		map[string]string{"target": "start", "replacement": "step1"},
		map[string]string{"target": "step1", "replacement": "step2"},
	))
	if res.IsError {
		t.Fatalf("顺序依赖应成立：%s", res.Content)
	}
	got, _ := tool.read("b.txt", 1, 0)
	if !strings.Contains(got.Content, "step2") || strings.Contains(got.Content, "step1") {
		t.Fatalf("最终内容应为 step2：%q", got.Content)
	}
}

// 原子性：第二段失败 → 整次失败，第一段也不落盘（文件零修改）。
func TestReplaceEdits_AnyMissFailsAtomically(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "c.txt", "keep me\n")
	res := replaceEdits(t, tool, editsArgs("c.txt",
		map[string]string{"target": "keep", "replacement": "CHANGED"},
		map[string]string{"target": "不存在的文本", "replacement": "x"},
	))
	if !res.IsError {
		t.Fatal("第二段零匹配必须整次失败")
	}
	if !strings.Contains(res.Content, "edits[1]") {
		t.Fatalf("错误应指明是哪一段：%q", res.Content)
	}
	got, _ := tool.read("c.txt", 1, 0)
	if !strings.Contains(got.Content, "keep me") || strings.Contains(got.Content, "CHANGED") {
		t.Fatalf("文件必须零修改：%q", got.Content)
	}
}

func TestReplaceEdits_MultiMatchPolicy(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "d.txt", "dup\nmid\ndup\n")
	// 默认拒绝多处匹配（与单段 C-FS-2 同语义）
	res := replaceEdits(t, tool, editsArgs("d.txt",
		map[string]string{"target": "dup", "replacement": "D"},
	))
	if !res.IsError || !strings.Contains(res.Content, "matches 2 locations") {
		t.Fatalf("多处匹配默认拒绝：%q", res.Content)
	}
	// allow_multiple 放行后两处都换
	args := editsArgs("d.txt", map[string]string{"target": "dup", "replacement": "D"})
	args["allow_multiple"] = true
	res = replaceEdits(t, tool, args)
	if res.IsError {
		t.Fatalf("放行后不应失败：%s", res.Content)
	}
	got, _ := tool.read("d.txt", 1, 0)
	if strings.Count(got.Content, "|D") != 2 {
		t.Fatalf("两处都应替换：%q", got.Content)
	}
}

func TestReplaceEdits_Guards(t *testing.T) {
	tool := newTool(t)
	mustWrite(t, tool, "e.txt", "x\n")
	// 与单段参数混用 → 拒绝
	args := editsArgs("e.txt", map[string]string{"target": "x", "replacement": "y"})
	args["target"] = "x"
	if res := replaceEdits(t, tool, args); !res.IsError {
		t.Fatal("edits 与 target/replacement 混用必须拒绝")
	}
	// 空白 target → 拒绝
	res := replaceEdits(t, tool, editsArgs("e.txt", map[string]string{"target": "  ", "replacement": "y"}))
	if !res.IsError || !strings.Contains(res.Content, "target is required") {
		t.Fatalf("空白 target 必须拒绝：%q", res.Content)
	}
	// 单段形态不受影响（回归）
	res = replaceEdits(t, tool, map[string]any{"action": "replace", "path": "e.txt", "target": "x", "replacement": "y"})
	if res.IsError {
		t.Fatalf("单段形态被改坏：%q", res.Content)
	}
}
