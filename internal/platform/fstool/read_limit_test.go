package fstool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestFS(t *testing.T, root string) *Tool {
	t.Helper()
	return New(root)
}

// 审计#6：fs.read 大小硬顶——超过 10MB 的文件拒绝整读并给出指引（防打满内存）。
func TestFSTool_ReadSizeHardLimit(t *testing.T) {
	root := t.TempDir()
	f := newTestFS(t, root)
	big := filepath.Join(root, "big.bin")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 11<<20)), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := f.Execute(context.Background(), json.RawMessage(`{"action":"read","path":"big.bin"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "too large") {
		t.Fatalf("超大文件应拒绝整读：%+v", res)
	}
	// 正常小文件不受影响
	small := filepath.Join(root, "small.txt")
	if err := os.WriteFile(small, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = f.Execute(context.Background(), json.RawMessage(`{"action":"read","path":"small.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.Content != "hello" {
		t.Fatalf("小文件读取应正常：%+v", res)
	}
}

// 审计#4：工作区内的符号链接指向区外时，读取必须拒绝（词法前缀检查拦不住）。
// Windows 创建符号链接需要特权：失败时跳过（非 Windows/有特权环境仍覆盖）。
func TestFSTool_SymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "leak.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("无法创建符号链接（需要特权）：", err)
	}
	f := newTestFS(t, root)
	res, err := f.Execute(context.Background(), json.RawMessage(`{"action":"read","path":"leak.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "escapes workspace") {
		t.Fatalf("区外符号链接应被拒绝：%+v", res)
	}
}
