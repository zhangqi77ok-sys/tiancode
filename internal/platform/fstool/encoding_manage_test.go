package fstool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var gbkZhongWen = []byte{0xd6, 0xd0, 0xce, 0xc4} // "中文" 的 GBK 编码

// 2026-10-19：二进制文件拒绝按文本读写（此前原始字节直接进模型上下文）。
func TestRead_BinaryRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), []byte{0x00, 0x01, 0xff, 'a', '\n'}, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := New(dir)
	out, isErr := runFS(t, tool, map[string]any{"action": "read", "path": "a.bin"})
	if !isErr || !strings.Contains(out, "二进制") {
		t.Fatalf("二进制文件应拒绝并说明：isErr=%v %q", isErr, out)
	}
	if out, isErr := runFS(t, tool, map[string]any{"action": "replace", "path": "a.bin", "target": "a", "replacement": "b"}); !isErr || !strings.Contains(out, "二进制") {
		t.Fatalf("二进制文件 replace 应拒绝：%q", out)
	}
}

// GBK 文件：read 转 UTF-8 并注明；replace 用 UTF-8 文本匹配、按 GBK 写回。
func TestGBK_ReadReplaceKeepsEncoding(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "g.txt")
	body := append(append([]byte("x="), gbkZhongWen...), '\n')
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := New(dir)
	out, isErr := runFS(t, tool, map[string]any{"action": "read", "path": "g.txt"})
	if isErr || !strings.Contains(out, "x=中文") || !strings.Contains(out, "GBK") {
		t.Fatalf("GBK 文件应转 UTF-8 显示并注明编码：%q", out)
	}
	if out, isErr := runFS(t, tool, map[string]any{"action": "replace", "path": "g.txt", "target": "中文", "replacement": "汉字"}); isErr {
		t.Fatalf("GBK 文件 replace 应成功：%s", out)
	}
	got, _ := os.ReadFile(p)
	want := append([]byte("x="), 0xba, 0xba, 0xd7, 0xd6, '\n') // "汉字" 的 GBK
	if string(got) != string(want) {
		t.Fatalf("应按 GBK 写回：% x", got)
	}
	// GBK 表示不了的字符：显式拒绝，文件不动
	if out, isErr := runFS(t, tool, map[string]any{"action": "replace", "path": "g.txt", "target": "汉字", "replacement": "😀"}); !isErr || !strings.Contains(out, "GBK") {
		t.Fatalf("GBK 无法表示的字符应拒绝：%q", out)
	}
	if again, _ := os.ReadFile(p); string(again) != string(want) {
		t.Fatal("拒绝时文件不得改动")
	}
}

// 带 BOM 的 UTF-8：整文件 write 后保留 BOM（模型给的内容通常不带 BOM）。
func TestWrite_KeepsUTF8BOM(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(p, []byte("\xef\xbb\xbfhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := New(dir)
	runFS(t, tool, map[string]any{"action": "read", "path": "b.txt"})
	if out, isErr := runFS(t, tool, map[string]any{"action": "write", "path": "b.txt", "content": "world\n"}); isErr {
		t.Fatal(out)
	}
	if got, _ := os.ReadFile(p); string(got) != "\xef\xbb\xbfworld\n" {
		t.Fatalf("应保留 BOM：%q", got)
	}
}

// fs.delete：删文件进检查点，撤回按原编码重建；非空目录拒绝；工作区根拒绝。
func TestDelete_RecoverableAndGuarded(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "g.txt")
	if err := os.WriteFile(p, gbkZhongWen, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "k.txt"), []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := New(dir)
	tool.BeginRound()
	if out, isErr := runFS(t, tool, map[string]any{"action": "delete", "path": "g.txt"}); isErr || !strings.Contains(out, "撤回本轮") {
		t.Fatalf("删除应成功并提示可撤回：%q", out)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("文件应已删除")
	}
	if out, isErr := runFS(t, tool, map[string]any{"action": "delete", "path": "sub"}); !isErr || !strings.Contains(out, "不是空目录") {
		t.Fatalf("非空目录应拒绝：%q", out)
	}
	if out, isErr := runFS(t, tool, map[string]any{"action": "delete", "path": "."}); !isErr {
		t.Fatalf("工作区根应拒绝：%q", out)
	}
	cps := tool.EndRound()
	if len(cps) != 1 {
		t.Fatalf("应有一个检查点：%+v", cps)
	}
	if err := tool.RestoreCheckpoint(cps[0]); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != string(gbkZhongWen) {
		t.Fatalf("撤回应按 GBK 原样重建：% x", got)
	}
}

// fs.move：目标已存在拒绝；移动后撤回 = 删除目标 + 重建源。
func TestMove_AndRevert(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "exists.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := New(dir)
	if out, isErr := runFS(t, tool, map[string]any{"action": "move", "path": "a.go", "to": "exists.go"}); !isErr || !strings.Contains(out, "已存在") {
		t.Fatalf("目标已存在应拒绝：%q", out)
	}
	tool.BeginRound()
	if out, isErr := runFS(t, tool, map[string]any{"action": "move", "path": "a.go", "to": "pkg/b.go"}); isErr {
		t.Fatal(out)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "pkg", "b.go")); string(got) != "package a\n" {
		t.Fatalf("目标内容不对：%q", got)
	}
	for _, cp := range tool.EndRound() {
		if err := tool.RestoreCheckpoint(cp); err != nil {
			t.Fatalf("撤回 %s：%v", cp.Path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pkg", "b.go")); !os.IsNotExist(err) {
		t.Fatal("撤回后目标应被删除")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.go")); string(got) != "package a\n" {
		t.Fatalf("撤回后源文件应重建：%q", got)
	}
}
