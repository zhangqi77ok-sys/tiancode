// 检查自愈循环（0.0.34）用例：触发条件（EndDone 且检查红）、预算封顶、
// 通过/用户消息重置、端到端（真 Send → 检查红 → 修复回合 → 检查绿）。
package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
)

type autofixRecorder struct {
	mu     sync.Mutex
	calls  []int // 每次回调的 attempt 值
	reason string
}

func (r *autofixRecorder) on(sessionID, reason string, attempt, max int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, attempt)
	r.reason = reason
}

func (r *autofixRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func autofixFixture(t *testing.T, out string, failed bool) (*ChatService, string) {
	t.Helper()
	dir := t.TempDir()
	oldPath := workspaceSettingsPath
	workspaceSettingsPath = func() string { return filepath.Join(dir, "workspace-settings.json") }
	t.Cleanup(func() { workspaceSettingsPath = oldPath })

	s := newChannelService(t, Config{})
	t.Cleanup(func() { s.Close() })
	root := t.TempDir()
	l, err := s.ledgerFor("s-autofix")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": root}); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceSettings(root, WorkspaceSettings{CheckCommand: "go test ./..."}); err != nil {
		t.Fatal(err)
	}
	oldRun := checkRunner
	checkRunner = func(_ context.Context, name string, args []string, wd string) (string, error) {
		if failed {
			return "foo.go:10: undefined: bar", errStub{}
		}
		return "ok", nil
	}
	t.Cleanup(func() { checkRunner = oldRun })
	return s, root
}

type errStub struct{}

func (errStub) Error() string { return "exit status 1" }

// 触发与预算：EndDone + 检查红 → 触发；第 3 次被预算挡下；检查绿后重置。
func TestAutoFix_TriggerBudgetReset(t *testing.T) {
	s, _ := autofixFixture(t, "", true)
	var rec autofixRecorder
	s.SetAutoFixHandler(rec.on)

	// EndDone：触发（attempt 1、2），第 3 次被预算挡下
	for i := 0; i < 3; i++ {
		s.afterTurnCheck("s-autofix", llm.EndDone)
	}
	if got := rec.count(); got != 2 {
		t.Fatalf("预算 2：应只触发 2 次，实际 %d", got)
	}
	if !strings.Contains(rec.reason, "自动定向修复") || !strings.Contains(rec.reason, "foo.go:10") {
		t.Fatalf("修复缘由应说明检查失败与位置：%q", rec.reason)
	}

	// 检查变绿 → 预算重置 → 再红 → 从 1 重新计
	oldRun := checkRunner
	checkRunner = func(_ context.Context, name string, args []string, wd string) (string, error) {
		return "ok", nil
	}
	s.afterTurnCheck("s-autofix", llm.EndDone) // 绿：重置
	checkRunner = oldRun
	s.afterTurnCheck("s-autofix", llm.EndDone)
	if got := rec.count(); got != 3 {
		t.Fatalf("重置后应再触发 1 次（总 3），实际 %d", got)
	}
	if last := rec.calls[len(rec.calls)-1]; last != 1 {
		t.Fatalf("重置后 attempt 应从 1 开始，实际 %d", last)
	}
}

// 用户中断/看门狗/错误收尾绝不触发自动修复——"停"就是停。
func TestAutoFix_NonDoneNeverTriggers(t *testing.T) {
	s, _ := autofixFixture(t, "", true)
	var rec autofixRecorder
	s.SetAutoFixHandler(rec.on)

	for _, r := range []llm.EndReason{llm.EndError, llm.EndCancelled} {
		s.afterTurnCheck("s-autofix", r)
	}
	if rec.count() != 0 {
		t.Fatalf("非 EndDone 不得触发自动修复，实际 %d 次", rec.count())
	}
}

// 端到端：真 Send（正常收尾）→ 检查红 → 自动修复回合（系统留痕落账本）→
// 修复回合收尾后检查变绿 → 预算重置、不再触发。
func TestAutoFix_EndToEnd(t *testing.T) {
	s, root := autofixFixture(t, "", true)

	// 检查序列：第 1 次（用户回合后）红，第 2 次（修复回合后）绿
	var checkMu sync.Mutex
	checkCalls := 0
	oldRun := checkRunner
	checkRunner = func(_ context.Context, name string, args []string, wd string) (string, error) {
		checkMu.Lock()
		defer checkMu.Unlock()
		checkCalls++
		if checkCalls == 1 {
			return "foo.go:10: undefined: bar", errStub{}
		}
		return "ok", nil
	}
	t.Cleanup(func() { checkRunner = oldRun })

	// 上游：每次调用都正常流式收尾（用户回合 + 修复回合共 2 次调用）
	var upstreamCalls sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Lock()
		calls++
		upstreamCalls.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	if _, err := s.AddChannel(llm.Channel{
		Name: "autofix", Protocol: llm.ProtocolOpenAI, BaseURL: srv.URL, Model: "m1", APIKey: "k",
	}); err != nil {
		t.Fatal(err)
	}

	// 生产同款接线：修复回调 = SendFixTurn + 持续消费（事件桥在测试里省略）。
	// fixDone 在修复回合**彻底收尾**后才关——主测试必须等它，否则 cleanup 的
	// s.Close 会赶在修复回合落盘前面跑（竞态实测：断言先完成、Close 先执行，
	// handler goroutine 才 Append → "ledger closed" 假错误）。
	fixStarted := make(chan struct{}, 1)
	fixDone := make(chan struct{})
	var rec autofixRecorder
	s.SetAutoFixHandler(func(sessionID, reason string, attempt, max int) {
		rec.on(sessionID, reason, attempt, max)
		fixStarted <- struct{}{}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ch, err := s.SendFixTurn(ctx, sessionID, reason)
		if err != nil {
			t.Errorf("SendFixTurn: %v", err)
			close(fixDone)
			return
		}
		for range ch { // 像 UI 一样持续消费
		}
		close(fixDone)
	})

	ch, err := s.Send(context.Background(), "s-autofix", "把功能写完")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	select {
	case <-fixStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("检查红后 10 秒内未触发自动修复回合")
	}
	// 修复回合结束后检查变绿：不再有第二次修复
	select {
	case <-fixDone:
	case <-time.After(15 * time.Second):
		t.Fatal("修复回合 15 秒内未收尾")
	}
	deadline := time.After(10 * time.Second)
	for rec.count() > 1 {
		select {
		case <-deadline:
			t.Fatal("修复回合收尾后应不再触发（检查已绿）")
		case <-time.After(50 * time.Millisecond):
		}
	}

	// 账本里必须有系统留痕（EventAssistantMsg "自动定向修复"），且无空用户消息
	ledger, err := s.ledgerFor("s-autofix")
	if err != nil {
		t.Fatal(err)
	}
	sawNote := false
	userMessages := 0
	if err := ledger.Replay(func(ev session.Event) error {
		switch ev.Kind() {
		case session.EventAssistantMsg:
			if strings.Contains(string(ev.Data()), "自动定向修复") {
				sawNote = true
			}
		case session.EventUserMessage:
			userMessages++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !sawNote {
		t.Fatal("修复回合应以 EventAssistantMsg 系统留痕落账本")
	}
	if userMessages != 1 {
		t.Fatalf("只应有一条用户消息（修复回合不写用户消息），实际 %d", userMessages)
	}
	_ = root
}
