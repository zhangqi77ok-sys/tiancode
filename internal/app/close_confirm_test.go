// 关窗拦截判据测试（0.0.21）：AnyRunning 只读反映 running 集合与用户命令行；
// ShouldConfirmClose 有轮次才拦、1 秒内连点不重发。
package app

import (
	"context"
	"runtime"
	"testing"
	"time"

	"tiancode/internal/core/session"
)

func TestShouldConfirmClose(t *testing.T) {
	s, _ := newMiniService(t)

	// 空闲：不拦
	if s.ShouldConfirmClose() {
		t.Fatal("没有运行中的会话不应拦截关窗")
	}
	if s.AnyRunning() {
		t.Fatal("空服务的 AnyRunning 必须为 false")
	}

	// 模拟一轮在跑：Send 前占位（直接写 running 集合，避免真发网络请求）
	s.mu.Lock()
	s.running["s-1"] = struct{}{}
	s.mu.Unlock()
	if !s.AnyRunning() {
		t.Fatal("running 非空时 AnyRunning 必须为 true")
	}

	// 第一次拦，1 秒内第二次不拦（确认框开着连点 X 不堆叠）
	if !s.ShouldConfirmClose() {
		t.Fatal("有轮次在跑必须拦截第一次关窗")
	}
	if s.ShouldConfirmClose() {
		t.Fatal("1 秒内的重复关窗不应重发事件")
	}

	// 轮次结束：恢复放行
	s.mu.Lock()
	delete(s.running, "s-1")
	s.mu.Unlock()
	if s.AnyRunning() {
		t.Fatal("轮次结束后 AnyRunning 必须为 false")
	}
	if s.ShouldConfirmClose() {
		t.Fatal("空闲后不应再拦截")
	}
}

// 0.0.21 实机补洞：用户命令行（RunUserCommand）不经过 Send 轮次——它的审批等待
// 与命令执行同样会被关窗打断，必须纳入 AnyRunning。
func TestShouldConfirmClose_UserCommandActive(t *testing.T) {
	s, ws := newMiniService(t)
	seedSession(t, s, "s-uc", ws)

	// 置位（模拟 RunUserCommand 已进入）：只判标志位，无需真发命令
	s.userCmdActive.Store(true)
	if !s.AnyRunning() {
		t.Fatal("用户命令行执行中 AnyRunning 必须为 true")
	}
	if !s.ShouldConfirmClose() {
		t.Fatal("用户命令行执行中必须拦截关窗")
	}
	s.userCmdActive.Store(false)
	if s.AnyRunning() {
		t.Fatal("命令行结束后 AnyRunning 必须为 false")
	}

	// 集成路径：审批闸门拦住 RunUserCommand（等待人确认）期间 AnyRunning 为 true，
	// 拒绝答复后复位。
	s.SetApprovalHandler(func(e ApprovalEvent) {}) // 收到事件不答复 = 等待中
	if err := s.SetApprovalPolicy([]string{"shell"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.RunUserCommand(context.Background(), "s-uc", "echo blocked")
	}()
	deadline := time.After(3 * time.Second)
	for {
		if s.AnyRunning() {
			break // 审批等待被 AnyRunning 观察到
		}
		select {
		case <-deadline:
			t.Fatal("审批等待期间 AnyRunning 必须为 true")
		default:
			runtime.Gosched()
			time.Sleep(10 * time.Millisecond)
		}
	}
	// 逐个拒绝挂起的审批，让调用收尾
	s.mu.Lock()
	ids := make([]string, 0, len(s.pendingApprovals))
	for id := range s.pendingApprovals {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_ = s.ResolveApproval(id, false, "test")
	}
	<-done
	if s.AnyRunning() {
		t.Fatal("审批答复后 AnyRunning 必须复位")
	}
}

// 防抖字段的存在不能影响既有账本行为——顺手锁一个只读断言。
func TestAnyRunning_DoesNotTouchLedger(t *testing.T) {
	s, _ := newMiniService(t)
	l, err := s.ledgerFor("s-any")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "hi"}); err != nil {
		t.Fatal(err)
	}
	_ = s.AnyRunning()
	if !s.sessionLedgerOnDisk("s-any") {
		t.Fatal("AnyRunning 不得影响账本可见性")
	}
}
