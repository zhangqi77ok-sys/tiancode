//go:build windows

package fstool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// CI（GitHub runner）的临时目录带 8.3 短名（RUNNER~1），本地通常是长名——
// 这个环境差异曾让 resolve 的前缀比对把整个工作区判成越界（全部 write 报
// "path escapes workspace: a.txt"，连 app 层的文件写用例一起变红）。
// 锁死契约：root 用短名构造时，New 必须把它解析成与候选路径同一形态，
// 相对路径的读/写照常工作。卷未启用 8.3 时 Skip（此时无法构造不一致环境）。
func TestResolveWithShortNameRoot(t *testing.T) {
	dir := t.TempDir()
	short, changed := shortNameOf(dir)
	if !changed || short == dir {
		t.Skipf("本环境无法构造短/长名不一致（卷未启用 8.3 或无差异）：%s", dir)
	}

	tool := New(short)
	if strings.EqualFold(tool.Root(), short) {
		t.Fatalf("New 未把短名 root 解析成真实路径：%q", tool.Root())
	}
	// 期望值必须是**长名**（CI 上 t.TempDir() 本身就是短名 RUNNER~1——
	// 拿它比对解析后的 root 就是在重演被修的 bug）
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(tool.Root(), real) {
		t.Fatalf("解析后的 root 应与原路径指向同一目录：%q vs %q", tool.Root(), real)
	}

	// 相对路径写/读必须照常工作（修复前：write 直接报 path escapes workspace）
	mustWrite(t, tool, "a.txt", "hello")
	got, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("短名 root 下落盘位置错误：content=%q err=%v", got, err)
	}
	if _, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{
		"action": "read", "path": "a.txt",
	})); err != nil {
		t.Fatalf("read: %v", err)
	}
}

// shortNameOf 返回路径的 8.3 短名形式（GetShortPathNameW）。
// changed=false 表示该路径没有更短的别名（卷禁用 8.3 命名）。
func shortNameOf(p string) (string, bool) {
	lp, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return p, false
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetShortPathNameW")
	n, _, _ := proc.Call(uintptr(unsafe.Pointer(lp)), 0, 0)
	if n == 0 {
		return p, false
	}
	buf := make([]uint16, n)
	m, _, _ := proc.Call(uintptr(unsafe.Pointer(lp)), uintptr(unsafe.Pointer(&buf[0])), n)
	if m == 0 || m > n {
		return p, false
	}
	short := syscall.UTF16ToString(buf[:m])
	return short, short != p
}
