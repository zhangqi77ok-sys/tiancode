package fstool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 0.0.07 操纵机构测试：write 整读门卫、撤销快照、replace 邻近行。
// 拒绝路径统一断言"原文件字节不变"。

func exec(t *testing.T, tool *Tool, v map[string]any) (bool, string, string) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	return res.IsError, res.Content, res.Diff
}

// 门卫：目标已存在但未整读 → write 拒绝且字节不变；整读后放行；新建不需要先读。
func TestWrite_GuardRequiresFullRead(t *testing.T) {
	tool := New(t.TempDir())
	original := "第一行\n第二行\n第三行\n"
	if err := os.WriteFile(filepath.Join(tool.Root(), "a.txt"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// 只读片段（start_line=1, line_count=1）→ write 必须拒绝，原文件字节不变
	isErr, content, _ := exec(t, tool, map[string]any{"action": "read", "path": "a.txt", "start_line": 1, "line_count": 1})
	if isErr {
		t.Fatalf("片段读取应成功：%s", content)
	}
	isErr, content, _ = exec(t, tool, map[string]any{"action": "write", "path": "a.txt", "content": "覆盖"})
	if !isErr {
		t.Fatal("未整读就 write 必须拒绝")
	}
	if !strings.Contains(content, "replace") || !strings.Contains(content, "整读") {
		t.Fatalf("拒绝信息必须写明出路：%s", content)
	}
	got, _ := os.ReadFile(filepath.Join(tool.Root(), "a.txt"))
	if string(got) != original {
		t.Fatalf("拒绝后文件字节改变：%q", got)
	}

	// 整读（读到文件尾）→ write 放行
	isErr, content, _ = exec(t, tool, map[string]any{"action": "read", "path": "a.txt"})
	if isErr {
		t.Fatalf("整读应成功：%s", content)
	}
	isErr, content, _ = exec(t, tool, map[string]any{"action": "write", "path": "a.txt", "content": "全覆盖内容"})
	if isErr {
		t.Fatalf("整读后 write 应成功：%s", content)
	}
	if got, _ := os.ReadFile(filepath.Join(tool.Root(), "a.txt")); string(got) != "全覆盖内容" {
		t.Fatalf("write 结果不符：%q", got)
	}

	// 新建文件的 write 不需要先读
	isErr, _, _ = exec(t, tool, map[string]any{"action": "write", "path": "new.txt", "content": "fresh"})
	if isErr {
		t.Fatal("新建文件不需要先读")
	}

	// replace 之后文件已变：此前的整读标记失效，write 再次拒绝
	isErr, _, _ = exec(t, tool, map[string]any{"action": "replace", "path": "a.txt", "target": "全覆盖内容", "replacement": "改过"})
	if isErr {
		t.Fatal("replace 应成功")
	}
	isErr, _, _ = exec(t, tool, map[string]any{"action": "write", "path": "a.txt", "content": "再覆盖"})
	if !isErr {
		t.Fatal("replace 后必须重新整读才能 write")
	}
}

// 撤销数据：write/replace 成功后带 Undo（旧全文 + 新内容哈希）；新建文件 OldExists=false。
func TestWriteReplace_UndoSnapshot(t *testing.T) {
	tool := New(t.TempDir())

	// 新建：OldExists=false，OldContent 空
	res, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "write", "path": "n.txt", "content": "v1"}))
	if err != nil || res.IsError {
		t.Fatalf("write: %v %s", err, res.Content)
	}
	if res.Undo == nil || res.Undo.OldExists {
		t.Fatalf("新建文件 Undo.OldExists 必须为 false：%+v", res.Undo)
	}
	if res.Undo.Path != "n.txt" || res.Undo.NewSHA256 == "" {
		t.Fatalf("Undo 字段缺失：%+v", res.Undo)
	}

	// replace：Undo 保存旧全文
	isErr, _, _ := exec(t, tool, map[string]any{"action": "replace", "path": "n.txt", "target": "v1", "replacement": "v2-longer"})
	if isErr {
		t.Fatal("replace 应成功")
	}
	// 再 write（整读过吗？replace 后标记失效，先整读）
	_, _, _ = exec(t, tool, map[string]any{"action": "read", "path": "n.txt"})
	res, err = tool.Execute(context.Background(), mustArgs(t, map[string]any{"action": "write", "path": "n.txt", "content": "v3"}))
	if err != nil || res.IsError {
		t.Fatalf("write v3: %v %s", err, res.Content)
	}
	if res.Undo == nil || !res.Undo.OldExists || res.Undo.OldContent != "v2-longer" {
		t.Fatalf("Undo 必须携带旧全文：%+v", res.Undo)
	}
	// Undo 不进模型可见 Content：模型看到的只有写回执 + 短 diff（1200 字节头尾
	// 截断预算），绝不能把整份旧文件塞进上下文。用足够长的旧内容验证 Content 长度有界。
	if len(res.Content) > 3*diffContentLimit+len(res.Undo.OldContent)/10 {
		t.Fatalf("疑似把整份旧文件塞进了模型可见 Content：len=%d", len(res.Content))
	}
}

// 零匹配：错误里带附近行（带行号），文件字节不变；无相近行时明说。
func TestReplace_ZeroMatchShowsNearbyLines(t *testing.T) {
	tool := New(t.TempDir())
	original := "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"
	if err := os.WriteFile(filepath.Join(tool.Root(), "a.go"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// target 首行与第 3 行相近（func main 拼错）
	isErr, content, _ := exec(t, tool, map[string]any{"action": "replace", "path": "a.go", "target": "func mainx() {", "replacement": "x"})
	if !isErr {
		t.Fatal("零匹配必须报错")
	}
	if !strings.Contains(content, "3|func main() {") {
		t.Fatalf("错误里必须能看到附近行（带行号）：%s", content)
	}
	if !strings.Contains(content, "file unchanged") {
		t.Fatalf("必须注明文件未动：%s", content)
	}
	if got, _ := os.ReadFile(filepath.Join(tool.Root(), "a.go")); string(got) != original {
		t.Fatalf("零匹配后文件字节改变：%q", got)
	}

	// 毫无相近内容：明说没有相近行
	isErr, content, _ = exec(t, tool, map[string]any{"action": "replace", "path": "a.go", "target": "zzz完全不相关的内容zzz", "replacement": "x"})
	if !isErr || !strings.Contains(content, "no similar line") {
		t.Fatalf("无相近行必须明说：%s", content)
	}
	if got, _ := os.ReadFile(filepath.Join(tool.Root(), "a.go")); string(got) != original {
		t.Fatalf("文件字节改变：%q", got)
	}
}

// read 正文带行号；整读不打印分段头；片段读带分段头。
func TestRead_LineNumbers(t *testing.T) {
	tool := New(t.TempDir())
	if err := os.WriteFile(filepath.Join(tool.Root(), "a.txt"), []byte("l1\nl2\nl3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	isErr, content, _ := exec(t, tool, map[string]any{"action": "read", "path": "a.txt"})
	if isErr {
		t.Fatalf("read: %s", content)
	}
	if !strings.Contains(content, "2|l2") || strings.Contains(content, "[start_line=") {
		t.Fatalf("整读应带行号且无分段头：%q", content)
	}
	isErr, content, _ = exec(t, tool, map[string]any{"action": "read", "path": "a.txt", "start_line": 2, "line_count": 1})
	if isErr {
		t.Fatalf("片段读: %s", content)
	}
	if !strings.Contains(content, "[start_line=2 读取 1 行 / 共 3 行]") || !strings.Contains(content, "2|l2") {
		t.Fatalf("片段读应带分段头与行号：%q", content)
	}
}
