package fstool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 0.0.10：write/replace 必须先确认再落盘。
//   - 未确认（gate 挂起）→ 文件字节不变；取消 = 按跳过，不写入；
//   - 确认 → 内容与 diff 一致；
//   - 确认前文件被外部改过 → 应用失败，原文件（外部内容）不动；
//   - 跳过 → 结果写明「未修改」。

type stubGate struct {
	// confirm 返回 (applied, err)；外部通过 ch 释放
	ch      chan struct{}
	applied bool
	err     error
	called  int
	lastP   EditProposal
	// hook 在 ConfirmEdit 入口（提案快照之后、Apply 之前）调用——模拟"确认期间外部改动"
	hook func()
}

func (g *stubGate) ConfirmEdit(ctx context.Context, p EditProposal) (bool, *UndoSnapshot, error) {
	g.called++
	g.lastP = p
	if g.hook != nil {
		g.hook()
	}
	if g.ch != nil {
		select {
		case <-g.ch:
		case <-ctx.Done():
			return false, nil, nil // 取消按跳过（不写入）
		}
	}
	if g.err != nil {
		return true, nil, g.err
	}
	if !g.applied {
		return false, nil, nil // 用户跳过：不调 Apply
	}
	u, err := p.Apply()
	if err != nil {
		return true, nil, err
	}
	return true, u, nil
}

func writeArgs(action, path, a, b string) map[string]any {
	if action == "write" {
		return map[string]any{"action": "write", "path": path, "content": b}
	}
	return map[string]any{"action": "replace", "path": path, "target": a, "replacement": b}
}

func runTool(t *testing.T, tool *Tool, v map[string]any) (bool, string) {
	t.Helper()
	res, err := tool.Execute(context.Background(), mustArgs(t, v))
	if err != nil {
		t.Fatal(err)
	}
	return res.IsError, res.Content
}

func TestEditGate_WriteNotConfirmedLeavesFileUntouched(t *testing.T) {
	tool := New(t.TempDir())
	g := &stubGate{ch: make(chan struct{})} // 挂起：用户不点
	tool.SetEditGate(g)
	p := filepath.Join(tool.Root(), "a.txt")
	if err := os.WriteFile(p, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 先整读（write 门卫）
	if isErr, _ := runTool(t, tool, map[string]any{"action": "read", "path": "a.txt"}); isErr {
		t.Fatal("read 应成功")
	}
	// ctx 超时取消 = 按跳过：需要 Execute 内部 ctx 有 deadline——直接给 Execute 传？Execute 自建 ctx。
	// 这里用不释放（ch 不关闭）+ 单独协程模拟：Execute 会一直挂起 → 用超时保护验证"不落盘"。
	done := make(chan struct{})
	go func() {
		runTool(t, tool, writeArgs("write", "a.txt", "", "覆盖内容"))
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("未确认时 Execute 不应返回（一直等用户）")
	case <-time.After(300 * time.Millisecond):
	}
	got, _ := os.ReadFile(p)
	if string(got) != "original" {
		t.Fatalf("未确认时文件字节改变：%q", got)
	}
	close(g.ch) // 用户取消（ctx 由外部终止的场景以 fstool 内部模拟：释放但 applied=false 路径见下）
	<-done
}

func TestEditGate_ConfirmAppliesDiffExactly(t *testing.T) {
	tool := New(t.TempDir())
	g := &stubGate{applied: true}
	tool.SetEditGate(g)
	if err := os.WriteFile(filepath.Join(tool.Root(), "a.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isErr, _ := runTool(t, tool, map[string]any{"action": "read", "path": "a.txt"}); isErr {
		t.Fatal("read 应成功")
	}
	isErr, content := runTool(t, tool, writeArgs("replace", "a.txt", "two", "TWO"))
	if isErr {
		t.Fatalf("replace 应成功：%s", content)
	}
	got, _ := os.ReadFile(filepath.Join(tool.Root(), "a.txt"))
	if string(got) != "one\nTWO\nthree\n" {
		t.Fatalf("确认后内容 = %q", got)
	}
	if g.lastP.Diff == "" || !strings.Contains(g.lastP.Diff, "-two") || !strings.Contains(g.lastP.Diff, "+TWO") {
		t.Fatalf("提案必须带短 diff：%q", g.lastP.Diff)
	}
}

func TestEditGate_ExternalChangeBlocksApply(t *testing.T) {
	tool := New(t.TempDir())
	g := &stubGate{applied: true}
	tool.SetEditGate(g)
	p := filepath.Join(tool.Root(), "a.txt")
	if err := os.WriteFile(p, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isErr, _ := runTool(t, tool, map[string]any{"action": "read", "path": "a.txt"}); isErr {
		t.Fatal("read 应成功")
	}
	// 确认前外部程序改了文件（改 size 与 mtime）
	g.err = nil
	g.hook = func() {
		future := time.Now().Add(2 * time.Second)
		os.Chtimes(p, future, future)
		if err := os.WriteFile(p, []byte("user-edit-longer"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	isErr, content := runTool(t, tool, writeArgs("replace", "a.txt", "v1", "v2"))
	if !isErr {
		t.Fatalf("外部改动必须导致应用失败：%s", content)
	}
	if !strings.Contains(content, "已被其他程序修改") {
		t.Fatalf("错误必须写明外部改动：%s", content)
	}
	if got, _ := os.ReadFile(p); string(got) != "user-edit-longer" {
		t.Fatalf("应用失败后文件不得被碰：%q", got)
	}
}

func TestEditGate_SkippedResultSaysUnmodified(t *testing.T) {
	tool := New(t.TempDir())
	g := &stubGate{applied: false}
	tool.SetEditGate(g)
	isErr, content := runTool(t, tool, writeArgs("write", "new.txt", "", "x"))
	if isErr {
		t.Fatalf("跳过不是错误：%s", content)
	}
	if !strings.Contains(content, "未修改") {
		t.Fatalf("跳过结果必须写明未修改：%s", content)
	}
	if _, err := os.Stat(filepath.Join(tool.Root(), "new.txt")); !os.IsNotExist(err) {
		t.Fatal("跳过后文件不得存在")
	}
}
