package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/session"
	"tiancode/internal/platform/fstool"
)

// runFS 执行一次 fs 工具调用并断言非业务失败（模拟模型侧的 write/replace，
// 只有它们进轮次检查点；用户手动「应用到文件」刻意不进——那是轮次之外的动作）。
func runFS(t *testing.T, tool *fstool.Tool, args map[string]any) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("fs 业务失败：%s", res.Content)
	}
}

// 第 6 批：回合检查点——应用到文件进账本（Replay 可见）、撤回本轮恢复多文件。

// 「应用到文件」写入成功即落账本：重启/切回会话后 Replay 仍能看到这张卡。
func TestProposeFileWrite_AppearsInReplay(t *testing.T) {
	ws := t.TempDir()
	s := newChannelService(t, Config{WorkDir: ws})
	defer s.Close()

	l, err := s.ledgerFor("s-ue")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": ws}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProposeFileWrite("s-ue", "a.txt", "hello"); err != nil {
		t.Fatal(err)
	}

	msgs, err := s.Replay("s-ue")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if m.Role == "tool" && m.Title == "a.txt" && m.Op == "write" {
			found = true
			if !strings.Contains(m.Content, "written a.txt") {
				t.Fatalf("回执卡内容不符：%q", m.Content)
			}
			if !strings.Contains(m.Diff, "+hello") {
				t.Fatalf("回执卡必须带 diff：%q", m.Diff)
			}
		}
	}
	if !found {
		t.Fatalf("Replay 必须能看到「应用到文件」的卡：%+v", msgs)
	}
}

// 撤回本轮：一个轮次里改两个文件（覆盖 + 新建），撤回后两者都恢复原状；
// 二次撤回明确报错（同一轮不会被撤回两次）。
func TestRevertRound_RestoresMultipleFiles(t *testing.T) {
	ws := t.TempDir()
	s := newChannelService(t, Config{WorkDir: ws})
	defer s.Close()
	if err := os.WriteFile(filepath.Join(ws, "keep.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := s.ledgerFor("s-rev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": ws}); err != nil {
		t.Fatal(err)
	}
	st, err := s.ensureSessionTools("s-rev", ws)
	if err != nil {
		t.Fatal(err)
	}

	// 模拟一个轮次：BeginRound → 模型改两个文件（write 新建 + replace 覆盖）
	// → EndRound → 落账本（sendCore 收尾同款）
	st.fs.BeginRound()
	runFS(t, st.fs, map[string]any{"action": "write", "path": "new.txt", "content": "created"})
	runFS(t, st.fs, map[string]any{"action": "replace", "path": "keep.txt", "target": "v1", "replacement": "v2"})
	cps := st.fs.EndRound()
	if len(cps) != 2 {
		t.Fatalf("检查点应含两个文件：%+v", cps)
	}
	if _, err := l.Append(session.EventRoundCheckpoint, map[string]any{"files": cps}); err != nil {
		t.Fatal(err)
	}

	// 撤回本轮
	res, err := s.RevertRound("s-rev")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 2 || len(res.Skipped) != 0 {
		t.Fatalf("应恢复两个文件且无跳过：%+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "keep.txt")); string(b) != "v1" {
		t.Fatalf("keep.txt 应恢复为 v1：%q", b)
	}
	if _, err := os.Stat(filepath.Join(ws, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("本轮新建的 new.txt 应被删除")
	}
	// 同一轮不会被撤回两次
	if _, err := s.RevertRound("s-rev"); err == nil {
		t.Fatal("重复撤回必须明确报错")
	}
}

// 撤回纪律：文件在本轮之后被手工改过 → 跳过并明确报告（绝不覆盖用户改动）。
func TestRevertRound_SkipsUserEditedFile(t *testing.T) {
	ws := t.TempDir()
	s := newChannelService(t, Config{WorkDir: ws})
	defer s.Close()

	l, err := s.ledgerFor("s-rev2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": ws}); err != nil {
		t.Fatal(err)
	}
	st, err := s.ensureSessionTools("s-rev2", ws)
	if err != nil {
		t.Fatal(err)
	}
	st.fs.BeginRound()
	runFS(t, st.fs, map[string]any{"action": "write", "path": "a.txt", "content": "agent-written"})
	cps := st.fs.EndRound()
	if _, err := l.Append(session.EventRoundCheckpoint, map[string]any{"files": cps}); err != nil {
		t.Fatal(err)
	}
	// 用户在本轮之后手工改了它
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("user-edited"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := s.RevertRound("s-rev2")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("应跳过被用户改过的文件：%+v", res)
	}
	if !strings.Contains(res.Skipped[0], "被改过") {
		t.Fatalf("跳过原因必须可读：%v", res.Skipped)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "a.txt")); string(b) != "user-edited" {
		t.Fatalf("不得覆盖用户改动：%q", b)
	}
}
