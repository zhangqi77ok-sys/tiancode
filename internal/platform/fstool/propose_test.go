package fstool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ProposeWrite（代码块「应用到文件」）直接落盘：回执带 diff 与新建标记。
// 0.0.10 的「应用/跳过」确认链路已移除——用户点「应用到文件」即写入。
func TestProposeWrite_NewFileAppliesDirectly(t *testing.T) {
	tool := New(t.TempDir())
	res, err := tool.ProposeWrite("n.txt", "hello\nworld")
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsNew {
		t.Fatalf("新建必须标记 IsNew：%+v", res)
	}
	if res.Bytes != len("hello\nworld") {
		t.Fatalf("Bytes = %d，期望 %d", res.Bytes, len("hello\nworld"))
	}
	if !strings.Contains(res.Diff, "+hello") {
		t.Fatalf("新建 diff 应为 +全文：%q", res.Diff)
	}
	got, err := os.ReadFile(filepath.Join(tool.Root(), "n.txt"))
	if err != nil || string(got) != "hello\nworld" {
		t.Fatalf("文件内容 = %q err=%v", got, err)
	}
}

// 覆盖已存在文件：绕过模型的整读门卫（用户显式指定路径 = 更高授权），
// 但 diff 必须体现旧内容的删除。
func TestProposeWrite_OverwriteExistingBypassesReadGuard(t *testing.T) {
	tool := New(t.TempDir())
	if err := os.WriteFile(filepath.Join(tool.Root(), "a.txt"), []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := tool.ProposeWrite("a.txt", "new content")
	if err != nil {
		t.Fatal(err)
	}
	if res.IsNew {
		t.Fatal("覆盖已有文件不得标记 IsNew")
	}
	if !strings.Contains(res.Diff, "-old content") || !strings.Contains(res.Diff, "+new content") {
		t.Fatalf("diff 必须体现增删：%q", res.Diff)
	}
	if got, _ := os.ReadFile(filepath.Join(tool.Root(), "a.txt")); string(got) != "new content" {
		t.Fatalf("文件内容 = %q", got)
	}
}

// 越界路径显式拒绝且不落盘（与模型的 write 共用同一 resolve 守卫）。
func TestProposeWrite_RejectsEscapePath(t *testing.T) {
	tool := New(t.TempDir())
	if _, err := tool.ProposeWrite("../escape.txt", "x"); err == nil {
		t.Fatal("越界路径必须拒绝")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(tool.Root()), "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("拒绝路径不得落盘")
	}
}
