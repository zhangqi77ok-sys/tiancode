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
	if res.IsError || res.Content != "1|hello" {
		t.Fatalf("小文件读取应正常（带行号）：%+v", res)
	}
}

// R3（0.0.07 行号化）：超大文件提供 start_line/line_count 按行分段读取作为合法
// 出路——整读被拒但不是死路（不把模型指去 shell 绕开上限）；多字节中文按整行
// 返回，绝不被字节切段截成乱码。
func TestFSTool_ReadLineSegments(t *testing.T) {
	root := t.TempDir()
	f := New(root)
	// 造一个 >10MB 的多行文件（每行约 100 字节，其中一行含中文）
	var b strings.Builder
	for i := 0; i < 110*1024; i++ {
		if i == 49999 {
			b.WriteString("中文行：多字节字符必须整行返回不得截半\n")
			continue
		}
		b.WriteString(strings.Repeat("abcdefghij", 10) + "\n")
	}
	big := filepath.Join(root, "big.txt")
	if err := os.WriteFile(big, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	// 整读拒绝，且指引按行分段（不是 shell）
	res, err := f.Execute(context.Background(), json.RawMessage(`{"action":"read","path":"big.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "start_line/line_count") {
		t.Fatalf("整读应拒绝并指引按行分段：%+v", res)
	}
	if strings.Contains(res.Content, "shell") {
		t.Fatal("不得把模型指去 shell 绕开读取上限")
	}
	// 按行分段可用，中文行完整（无替换符、无半字）
	res, err = f.Execute(context.Background(), json.RawMessage(`{"action":"read","path":"big.txt","start_line":50000,"line_count":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("按行分段应可用：%s", res.Content)
	}
	if !strings.Contains(res.Content, "50000|中文行：多字节字符必须整行返回不得截半") {
		t.Fatalf("中文行必须整行返回：%.200s", res.Content)
	}
	if strings.Contains(res.Content, "\ufffd") {
		t.Fatal("出现替换符 = 多字节字符被字节切段截断")
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
