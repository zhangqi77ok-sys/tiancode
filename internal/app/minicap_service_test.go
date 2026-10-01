// 0.3 最小能力的编排层测试：Replay 尾屏分页、用户命令行（含审批）、受控提交。
package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"tiancode/internal/core/session"
	"tiancode/internal/platform/gittool"
)

// newMiniService 构造隔离的编排层实例（渠道文件/数据目录/工作区全部临时）。
// 工作区必须已存在（启动期校验硬前提）。
func newMiniService(t *testing.T) (*ChatService, string) {
	t.Helper()
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := NewChatService(Config{
		DataDir:      filepath.Join(dir, "data"),
		WorkDir:      ws,
		ChannelsPath: filepath.Join(dir, "channels.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, ws
}

// seedSession 给服务落一个"有归属"的会话账本（user 消息 + workspace 事件）。
func seedSession(t *testing.T, s *ChatService, id, ws string) {
	t.Helper()
	l, err := s.ledgerFor(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "hi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": ws}); err != nil {
		t.Fatal(err)
	}
}

// ReplayTail 只给最后一屏，ReplayOlder 按锚点往前翻页；两段拼起来等于全量投影。
func TestReplayPages_TailThenOlder(t *testing.T) {
	s, _ := newMiniService(t)
	l, err := s.ledgerFor("s-page")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := l.Append(session.EventUserMessage, map[string]string{"text": string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}

	tail, err := s.ReplayTail("s-page", 2)
	if err != nil {
		t.Fatal(err)
	}
	if tail.Total != 6 || len(tail.Messages) != 2 || tail.From != 4 {
		t.Fatalf("尾屏 = total %d / from %d / %d 条，want 6/4/2", tail.Total, tail.From, len(tail.Messages))
	}
	if tail.Messages[0].Content != "e" || tail.Messages[1].Content != "f" {
		t.Fatalf("尾屏应是最后两条，got %q", []string{tail.Messages[0].Content, tail.Messages[1].Content})
	}

	older, err := s.ReplayOlder("s-page", 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if older.From != 2 || len(older.Messages) != 2 || older.Messages[0].Content != "c" {
		t.Fatalf("翻页锚点错位：%+v", older)
	}

	// 翻到头：from=2 再翻一页 → from=0，且前面没有了
	head, err := s.ReplayOlder("s-page", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if head.From != 0 || len(head.Messages) != 2 {
		t.Fatalf("翻到头的切片不合法：%+v", head)
	}
}

// 用户命令行：审批关闭时直接执行（复用会话 shell 工具）；会话不存在显式报错。
// 注意新装默认审批清单含 shell（0.2.36 审计 R3）——这里显式关闸再测直执行。
func TestRunUserCommand_DirectRun(t *testing.T) {
	s, ws := newMiniService(t)
	seedSession(t, s, "s-cmd", ws)
	if err := s.SetApprovalPolicy(nil); err != nil {
		t.Fatal(err)
	}

	res, err := s.RunUserCommand(context.Background(), "s-cmd", "echo hello-mini")
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || res.Denied {
		t.Fatalf("审批关闭应直接执行：%+v", res)
	}
	if !strings.Contains(res.Output, "hello-mini") {
		t.Fatalf("输出应含命令回显：%q", res.Output)
	}

	// 不存在的会话：显式报错，绝不静默
	if _, err := s.RunUserCommand(context.Background(), "s-nope", "echo x"); err == nil {
		t.Fatal("不存在的会话必须报错")
	}
}

// 用户命令行：审批闸门含 shell 时与模型命令走同一张审批卡；拒绝则不执行。
func TestRunUserCommand_ApprovalGate(t *testing.T) {
	s, ws := newMiniService(t)
	seedSession(t, s, "s-gate", ws)

	var mu sync.Mutex
	var got []ApprovalEvent
	s.SetApprovalHandler(func(e ApprovalEvent) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})
	if err := s.SetApprovalPolicy([]string{"shell"}); err != nil {
		t.Fatal(err)
	}

	// 异步等审批卡到达后拒绝：RunUserCommand 应返回 Denied，而不是执行命令
	go func() {
		for {
			mu.Lock()
			n := len(got)
			mu.Unlock()
			if n > 0 {
				mu.Lock()
				id := got[0].ID
				mu.Unlock()
				_ = s.ResolveApproval(id, false, "不许跑")
				return
			}
		}
	}()

	res, err := s.RunUserCommand(context.Background(), "s-gate", "echo should-not-run")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Denied || !res.IsError {
		t.Fatalf("被拒绝的命令必须带 Denied 标记：%+v", res)
	}
	if strings.Contains(res.Output, "should-not-run") {
		t.Fatalf("拒绝后不得有命令输出：%q", res.Output)
	}
}

// 受控提交：add -A + commit 在真实临时仓库上落一条提交；空说明显式拒绝。
func TestGitStageAndCommit_RealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	s, ws := newMiniService(t)
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@local")
	run("config", "user.name", "t")
	seedSession(t, s, "s-git", ws)

	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 空说明显式拒绝（绝不编造默认说明）
	if _, err := s.GitStageAndCommit("s-git", "   "); err == nil {
		t.Fatal("空说明必须被拒绝")
	}
	out, err := s.GitStageAndCommit("s-git", "mini: 第一条提交")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "mini: 第一条提交") {
		t.Fatalf("commit 输出应回显说明：%q", out)
	}
	// 提交后工作区应干净（status 只剩分支头行）
	status, err := gittool.StatusShort(ws)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.TrimSpace(status), "\n") > 0 {
		t.Fatalf("提交后仍有未提交变更：%q", status)
	}
}
