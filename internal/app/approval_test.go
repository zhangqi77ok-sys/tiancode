package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"tiancode/internal/core/agent"
)

// 审批桥接：默认关 → 清单匹配 → 发事件挂起 → 答复解除 → 重复/未知 ID 拒绝。
func TestChatService_ApprovalBridge(t *testing.T) {
	s := newChannelService(t, Config{})

	// 默认关（ADR-0007 第 1 条：不配置就不干预）
	if got := s.ApprovalPolicy(); len(got) != 0 {
		t.Fatalf("默认策略应为空，实际 %v", got)
	}

	// 开启并清洗输入（空白项丢弃）
	if err := s.SetApprovalPolicy([]string{" shell ", "", "  "}); err != nil {
		t.Fatal(err)
	}
	if got := s.ApprovalPolicy(); len(got) != 1 || got[0] != "shell" {
		t.Fatalf("策略清洗结果不对：%v", got)
	}
	// 返回副本：外部改动不得影响内部状态
	got := s.ApprovalPolicy()
	got[0] = "被篡改"
	if s.ApprovalPolicy()[0] != "shell" {
		t.Fatal("ApprovalPolicy 必须返回副本")
	}

	events := make(chan ApprovalEvent, 1)
	s.SetApprovalHandler(func(e ApprovalEvent) { events <- e })
	ap := &uiApprover{svc: s}

	// 不在清单内的工具：直接放行且不发事件（绝不做泛化拦截）
	d, err := ap.Review(context.Background(), agent.ApprovalRequest{ToolName: "fs", Arguments: "{}"})
	if err != nil || !d.Approved {
		t.Fatalf("清单外工具应直接放行：%+v err=%v", d, err)
	}
	select {
	case e := <-events:
		t.Fatalf("清单外工具不应发审批事件：%+v", e)
	default:
	}

	// 在清单内：发事件并挂起
	type result struct {
		d   agent.Decision
		err error
	}
	done := make(chan result, 1)
	go func() {
		dec, rerr := ap.Review(context.Background(), agent.ApprovalRequest{
			ToolName: "shell", Arguments: `{"command":"echo hi"}`,
		})
		done <- result{dec, rerr}
	}()

	var ev ApprovalEvent
	select {
	case ev = <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("超时未收到审批事件")
	}
	if ev.ID == "" || ev.ToolName != "shell" || ev.Arguments != `{"command":"echo hi"}` {
		t.Fatalf("事件内容不符（参数必须原样透传）：%+v", ev)
	}
	select {
	case r := <-done:
		t.Fatalf("答复前不应返回：%+v", r)
	default:
	}

	// 答复（拒绝）→ 立即返回，原因原样带回
	if err := s.ResolveApproval(ev.ID, false, "测试拒绝"); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.d.Approved || r.d.Reason != "测试拒绝" {
			t.Fatalf("答复结果不符：%+v err=%v", r.d, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("答复后应立即返回")
	}

	// 已处理 ID / 未知 ID 一律报错（不静默放行）
	if err := s.ResolveApproval(ev.ID, true, ""); err == nil {
		t.Fatal("重复处理同一 ID 应报错")
	}
	if err := s.ResolveApproval("ap-unknown", true, ""); err == nil {
		t.Fatal("未知 ID 应报错")
	}
}

// 策略持久化：重启（重新装配）后仍是开启状态——否则用户每次启动都要重开开关。
func TestChatService_ApprovalPolicyPersists(t *testing.T) {
	channelsPath := filepath.Join(t.TempDir(), "channels.json")
	work := t.TempDir()
	mk := func() *ChatService {
		s, err := NewChatService(Config{
			DataDir:      t.TempDir(),
			WorkDir:      work,
			ChannelsPath: channelsPath,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}

	s1 := mk()
	if err := s1.SetApprovalPolicy([]string{"shell"}); err != nil {
		t.Fatal(err)
	}

	// 重新装配 = 模拟重启
	s2 := mk()
	got := s2.ApprovalPolicy()
	if len(got) != 1 || got[0] != "shell" {
		t.Fatalf("重启后策略丢失：%v", got)
	}

	// 关闭同样要落盘：Save 写整份配置，旧值必须被清除（否则"关了又自己开"）
	if err := s2.SetApprovalPolicy(nil); err != nil {
		t.Fatal(err)
	}
	s3 := mk()
	if len(s3.ApprovalPolicy()) != 0 {
		t.Fatalf("关闭后重启仍为开：%v", s3.ApprovalPolicy())
	}
}
func TestChatService_ApprovalCancelWhileWaiting(t *testing.T) {
	s := newChannelService(t, Config{})
	if err := s.SetApprovalPolicy([]string{"shell"}); err != nil {
		t.Fatal(err)
	}
	ap := &uiApprover{svc: s}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	if _, err := ap.Review(ctx, agent.ApprovalRequest{ToolName: "shell"}); err == nil {
		t.Fatal("取消时 Review 必须返回错误（agent 据此视为拒绝）")
	}

	s.mu.Lock()
	pending := len(s.pendingApprovals)
	s.mu.Unlock()
	if pending != 0 {
		t.Fatalf("取消后未决请求泄漏：%d", pending)
	}
}
