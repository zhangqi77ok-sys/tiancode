package fstool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 端到端接线：工具结果必须携带 diff（UI 变更卡片的数据源），
// 且"内容无变化"时为空——避免 UI 弹出空 diff 卡片。
func TestTool_WriteAndReplace_IncludeDiff(t *testing.T) {
	tool := New(t.TempDir())
	ctx := context.Background()
	args := func(m map[string]any) json.RawMessage {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	writeArgs := map[string]any{"action": "write", "path": "a.txt", "content": "one\ntwo\n"}

	res, err := tool.Execute(ctx, args(writeArgs))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Diff, "+one") {
		t.Fatalf("write diff = %q", res.Diff)
	}
	// 相同内容再写一次 → 无变化，diff 必须为空
	resSame, err := tool.Execute(ctx, args(writeArgs))
	if err != nil {
		t.Fatal(err)
	}
	if resSame.Diff != "" {
		t.Fatalf("unchanged write diff = %q, want empty", resSame.Diff)
	}

	resRep, err := tool.Execute(ctx, args(map[string]any{
		"action": "replace", "path": "a.txt", "target": "two", "replacement": "TWO",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resRep.Diff, "-two") || !strings.Contains(resRep.Diff, "+TWO") {
		t.Fatalf("replace diff = %q", resRep.Diff)
	}
}

// 逐行 diff：上下文保留 + 变更行标注（覆盖"改一行"这类最常见编辑）。
func TestDiffText_LineChange(t *testing.T) {
	old := "line1\nline2\nline3\n"
	updated := "line1\nLINE-TWO\nline3\n"
	d := diffText("f.txt", old, updated)
	for _, want := range []string{"f.txt", "-line2", "+LINE-TWO", " line1"} {
		if !strings.Contains(d, want) {
			t.Fatalf("diff missing %q:\n%s", want, d)
		}
	}
}

// 内容无变化 → 空串（调用方据此不推事件，避免 UI 出现空 diff 卡片）。
func TestDiffText_NoChange(t *testing.T) {
	if d := diffText("f.txt", "same\n", "same\n"); d != "" {
		t.Fatalf("diff = %q, want empty", d)
	}
}

// 新建文件（旧内容为空）→ 全为新增行。
func TestDiffText_NewFile(t *testing.T) {
	d := diffText("new.txt", "", "a\nb\n")
	if !strings.Contains(d, "+a") || !strings.Contains(d, "+b") {
		t.Fatalf("new file diff = %q", d)
	}
}

// 超长 diff 必须截断并标注（工具卡片是辅助确认，不是完整审计——全文在账本里）。
func TestDiffText_Truncates(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < maxDiffLines*2; i++ {
		sb.WriteString("x\n")
	}
	d := diffText("big.txt", "", sb.String())
	if !strings.Contains(d, "已截断") {
		t.Fatalf("expected truncation notice, got %d bytes", len(d))
	}
	if lines := strings.Count(d, "\n"); lines > maxDiffLines+5 {
		t.Fatalf("diff not truncated: %d lines", lines)
	}
}
