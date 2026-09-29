package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/llm"
)

// 0.2.35 审计修复的回归测试。逐项对应审计编号。

// 审计#1：工具根跟会话走——A 会话在 dirA 发过消息（账本记录归属）后，
// 即使全局工作区切到 dirB，再向 A 发送也必须切回 dirA（后台会话的排队
// 续发不能改到别的项目）。
func TestChatService_SendSwitchesBackToSessionWorkspace(t *testing.T) {
	newTestUpstream(t, 0)
	dirA := t.TempDir()
	dirB := t.TempDir()
	s := newChannelService(t, Config{WorkDir: dirA})
	defer s.Close()
	if _, err := s.AddChannel(llm.Channel{
		Name: "up", Protocol: llm.ProtocolOpenAI, BaseURL: "http://127.0.0.1:1", Model: "m1", APIKey: "k",
	}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// 会话 s1 首聊：落账本归属 dirA
	ch, err := s.Send(ctx, "s-ws", "hi")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}

	// 全局切到 dirB（用户点开了别的项目的会话）
	if err := s.SetWorkspace(dirB); err != nil {
		t.Fatal(err)
	}
	if s.Workspace() != filepath.Clean(dirB) {
		t.Fatalf("前置：工作区应已切到 dirB，got %q", s.Workspace())
	}

	// 再向 s1 发送：必须切回 dirA（后台会话的排队续发不能改到别的项目）
	if _, err := s.Send(ctx, "s-ws", "hi again"); err != nil {
		t.Fatal(err)
	}
	if got := s.Workspace(); got != filepath.Clean(dirA) {
		t.Fatalf("发送后工作区 = %q, want 会话归属 %q（审计#1）", got, filepath.Clean(dirA))
	}
}

// 审计#5：工作区路径规范化——尾部反斜杠不再让 fs 的前缀比较全部误判越界。
func TestChatService_WorkspaceTrailingSlashNormalized(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	dir := t.TempDir()
	// 尾部反斜杠（Windows 用户常见输入）
	if err := s.SetWorkspace(dir + string(filepath.Separator)); err != nil {
		t.Fatal(err)
	}
	got := s.Workspace()
	if strings.HasSuffix(got, `\`) || strings.HasSuffix(got, `/`) {
		t.Fatalf("工作区应去掉尾部反斜杠：%q", got)
	}
	if got != filepath.Clean(dir) {
		t.Fatalf("规范化后应与 Clean 一致：%q vs %q", got, filepath.Clean(dir))
	}
}

// 审计#7：审批清单是本回合快照——approverFor 之后再改全局策略，
// 本回合的审批器行为不变（"进行中的轮次维持开跑时的策略"这才成立）。
func TestChatService_ApprovalSnapshotNotLiveList(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	if err := s.SetApprovalPolicy([]string{"shell"}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	approver := s.approverFor("s-snap")
	s.mu.Unlock()
	ui, ok := approver.(*uiApprover)
	if !ok {
		t.Fatalf("approverFor 应返回 *uiApprover")
	}
	if len(ui.allowed) != 1 || ui.allowed[0] != "shell" {
		t.Fatalf("快照 = %v, want [shell]", ui.allowed)
	}
	// 全局策略变更：快照不受影响
	if err := s.SetApprovalPolicy([]string{}); err != nil {
		t.Fatal(err)
	}
	if len(ui.allowed) != 1 || ui.allowed[0] != "shell" {
		t.Fatalf("策略变更后快照被污染：%v", ui.allowed)
	}
}
