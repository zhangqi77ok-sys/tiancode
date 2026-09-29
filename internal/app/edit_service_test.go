package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/session"
)

// 0.0.10：chatEditGate 往返——fs write 触发确认事件 → ResolveEdit(true) 应用落盘；
// ResolveEdit(false) 跳过（结果写明未修改）；会话取消按跳过（文件不动）。
func TestEditGate_EndToEndWithService(t *testing.T) {
	ws := t.TempDir()
	s := newChannelService(t, Config{WorkDir: ws})
	defer s.Close()

	// 建会话归属（账本首个 workspace 事件）→ ensureSessionTools 注入 gate
	l, err := s.ledgerFor("s-edit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": ws}); err != nil {
		t.Fatal(err)
	}
	st, err := s.ensureSessionTools("s-edit", ws)
	if err != nil {
		t.Fatal(err)
	}

	// 交接用带缓冲 channel（共享变量无同步边，CI 上轮询方可能永远观察不到写入——实测翻过车）
	editCh := make(chan EditEvent, 1)
	s.SetEditHandler(func(e EditEvent) { editCh <- e })

	// --- 应用路径：异步执行 write，主协程收到事件后 ResolveEdit(true) ---
	done := make(chan struct{})
	var resErrStr string
	var resIsErr bool
	go func() {
		defer close(done)
		res, err := st.fs.Execute(context.Background(), mustJSON(t, map[string]any{
			"action": "write", "path": "n.txt", "content": "confirmed content",
		}))
		if err != nil {
			t.Error(err)
			return
		}
		resIsErr, resErrStr = res.IsError, res.Content
	}()
	var got EditEvent
	select {
	case got = <-editCh:
	case <-time.After(2 * time.Second):
		t.Fatal("未收到确认事件")
	}
	if got.Path != "n.txt" || !strings.Contains(got.Diff, "+confirmed content") {
		t.Fatalf("确认事件内容不符：path=%q diff=%q", got.Path, got.Diff)
	}
	if err := s.ResolveEdit("s-edit", got.ID, true); err != nil {
		t.Fatal(err)
	}
	<-done
	if resIsErr {
		t.Fatalf("应用后不应报错：%s", resErrStr)
	}
	b, err := os.ReadFile(filepath.Join(ws, "n.txt"))
	if err != nil || string(b) != "confirmed content" {
		t.Fatalf("确认后文件内容 = %q err=%v", b, err)
	}

	// --- 跳过路径：ResolveEdit(false) → 结果写明未修改，文件不动 ---
	skipCh := make(chan EditEvent, 1)
	s.SetEditHandler(func(e EditEvent) { skipCh <- e })
	done2 := make(chan struct{})
	var skipContent string
	var skipIsErr bool
	go func() {
		defer close(done2)
		res, err := st.fs.Execute(context.Background(), mustJSON(t, map[string]any{
			"action": "write", "path": "n.txt", "content": "第二版",
		}))
		if err != nil {
			t.Error(err)
			return
		}
		skipIsErr, skipContent = res.IsError, res.Content
	}()
	var ev EditEvent
	select {
	case ev = <-skipCh:
	case <-time.After(2 * time.Second):
		t.Fatal("未收到第二个确认事件")
	}
	if err := s.ResolveEdit("s-edit", ev.ID, false); err != nil {
		t.Fatal(err)
	}
	<-done2
	if skipIsErr || !strings.Contains(skipContent, "未修改") {
		t.Fatalf("跳过结果应写明未修改：isErr=%v content=%q", skipIsErr, skipContent)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "n.txt")); string(b) != "confirmed content" {
		t.Fatalf("跳过后文件不得被改：%q", b)
	}

	// --- 取消路径：ctx 取消 = 按跳过 ---
	done3 := make(chan struct{})
	go func() {
		defer close(done3)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _ = st.fs.Execute(ctx, mustJSON(t, map[string]any{
			"action": "write", "path": "n.txt", "content": "第三版",
		}))
	}()
	select {
	case <-done3:
	case <-time.After(2 * time.Second):
		t.Fatal("取消后 Execute 应立即返回（按跳过）")
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "n.txt")); string(b) != "confirmed content" {
		t.Fatalf("取消后文件不得被改：%q", b)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
