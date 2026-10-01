package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/session"
)

// 「应用到文件」直接落盘（0.0.10 的确认卡链路已移除——改了就是改了）：
// 有归属工作区的会话写盘成功并返回回执（diff/新建标记）；
// 覆盖已有文件不因"没整读过"被拒（用户显式指定路径 = 更高授权）；
// 纯对话会话（无工作区）显式报错，不静默。
func TestProposeFileWrite_AppliesDirectly(t *testing.T) {
	ws := t.TempDir()
	s := newChannelService(t, Config{WorkDir: ws})
	defer s.Close()

	// 建会话归属（账本首个 workspace 事件）：sessionWorkspace 以此为根
	l, err := s.ledgerFor("s-write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": ws}); err != nil {
		t.Fatal(err)
	}

	// --- 新建：IsNew=true，diff 为 +全文，字节数与内容落盘 ---
	res, err := s.ProposeFileWrite("s-write", "n.txt", "package main\n")
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsNew || res.Bytes != len("package main\n") || !strings.Contains(res.Diff, "+package main") {
		t.Fatalf("新建回执不符：%+v", res)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "n.txt")); err != nil || string(b) != "package main\n" {
		t.Fatalf("写盘内容 = %q err=%v", b, err)
	}

	// --- 覆盖：IsNew=false，diff 必须体现新增行 ---
	res2, err := s.ProposeFileWrite("s-write", "n.txt", "package main\n\nfunc main() {}\n")
	if err != nil {
		t.Fatal(err)
	}
	if res2.IsNew || !strings.Contains(res2.Diff, "+func main() {}") {
		t.Fatalf("覆盖回执不符：%+v", res2)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "n.txt")); string(b) != "package main\n\nfunc main() {}\n" {
		t.Fatalf("覆盖后内容 = %q", b)
	}

	// --- 无工作区（纯对话）：显式报错，不静默 ---
	if _, err := s.ProposeFileWrite("s-plain", "x.txt", "x"); err == nil {
		t.Fatal("无工作区必须报错")
	}
	if _, err := os.Stat(filepath.Join(ws, "x.txt")); !os.IsNotExist(err) {
		t.Fatal("报错路径不得落盘")
	}
}

// 已删除的会话：「应用到文件」必须显式报错，且绝不重建工具集（第二轮体检 R3：
// 点旧会话的"应用"曾复活无主 fs/shell，挂到应用退出）。
func TestProposeFileWrite_DeletedSessionRejected(t *testing.T) {
	ws := t.TempDir()
	s := newChannelService(t, Config{WorkDir: ws})
	defer s.Close()

	l, err := s.ledgerFor("s-pf-gone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": ws}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession("s-pf-gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProposeFileWrite("s-pf-gone", "a.txt", "x"); err == nil || !strings.Contains(err.Error(), "已删除") {
		t.Fatalf("已删除会话必须显式报错：%v", err)
	}
	s.sessMu.Lock()
	_, alive := s.sessTools["s-pf-gone"]
	s.sessMu.Unlock()
	if alive {
		t.Fatal("不得为已删除会话重建工具集")
	}
}
