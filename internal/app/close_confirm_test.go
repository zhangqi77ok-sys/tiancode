// 关窗拦截判据测试（0.0.21）：AnyRunning 只读反映 running 集合；
// ShouldConfirmClose 有轮次才拦、1 秒内连点不重发。
package app

import (
	"testing"

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
