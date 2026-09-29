package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/agent"
	"tiancode/internal/core/llm"
)

// 0.2.35 审计修复的回归测试。逐项对应审计编号。

// 0.2.36 审计 R1：工具根按会话持有——A（dirA）与 B（dirB）各写各的项目，
// 互不串根；全局 Workspace（顶栏"新会话默认"值）不因发送被改写。
// 这替换了 0.2.35 的"发送前切全局根"方案（并行竞态 + 误杀另一路 shell）。
func TestChatService_SessionToolsArePerSession(t *testing.T) {
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
	drain := func(id string) {
		t.Helper()
		ch, err := s.Send(context.Background(), id, "hi")
		if err != nil {
			t.Fatal(err)
		}
		for range ch {
		}
	}
	rootOf := func(id string) string {
		t.Helper()
		s.mu.Lock()
		defer s.mu.Unlock()
		st, ok := s.sessTools[id]
		if !ok {
			t.Fatalf("会话 %s 无工具集", id)
		}
		return st.root
	}

	// 会话 A 首聊：归属 dirA（= 当时顶栏值）
	drain("s-A")
	if got := rootOf("s-A"); got != normalizeWorkspace(dirA) {
		t.Fatalf("A 根 = %q, want %q", got, normalizeWorkspace(dirA))
	}

	// 用户在顶栏切到 dirB（只影响"新会话默认"，不碰 A）
	if err := s.SetWorkspace(dirB); err != nil {
		t.Fatal(err)
	}

	// 会话 B 首聊：归属 dirB
	drain("s-B")
	if got := rootOf("s-B"); got != normalizeWorkspace(dirB) {
		t.Fatalf("B 根 = %q, want %q", got, normalizeWorkspace(dirB))
	}

	// 关键断言：再向 A 发送（此时顶栏是 dirB）——A 的根仍是 dirA（不串根）
	drain("s-A")
	if got := rootOf("s-A"); got != normalizeWorkspace(dirA) {
		t.Fatalf("再次发送后 A 根 = %q, want %q（不得串根）", got, normalizeWorkspace(dirA))
	}
	// 顶栏值保持用户选择（不被发送改写）
	if got := s.Workspace(); got != normalizeWorkspace(dirB) {
		t.Fatalf("顶栏工作区 = %q, want %q（发送不应改写全局）", got, normalizeWorkspace(dirB))
	}
}

// 纯对话会话的空工作区必须保留：不因"没有记录"套上一个默认目录。
func TestChatService_EmptyWorkspaceSessionStaysEmpty(t *testing.T) {
	newTestUpstream(t, 0)
	s := newChannelService(t, Config{})
	defer s.Close()
	// 显式回到纯对话（测试助手默认会给一个临时工作区）
	if err := s.SetWorkspace(""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddChannel(llm.Channel{
		Name: "up", Protocol: llm.ProtocolOpenAI, BaseURL: "http://127.0.0.1:1", Model: "m1", APIKey: "k",
	}); err != nil {
		t.Fatal(err)
	}
	ch, err := s.Send(context.Background(), "s-empty", "hi")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	s.mu.Lock()
	st := s.sessTools["s-empty"]
	s.mu.Unlock()
	if st == nil || st.root != "" {
		t.Fatalf("纯对话会话根应为空（本地工具下线）：%+v", st)
	}
	if st.fs != nil || st.shell != nil {
		t.Fatal("空根不得构造本地工具（安全红线）")
	}
}

// R3：审批独立超时按**拒绝**处理（绝不放行）——后台会话的确认卡不会无限
// 挂住整轮，且"没确认就不执行"的保护默认在。
func TestChatService_ApprovalTimeoutDenies(t *testing.T) {
	old := approvalReplyTimeout
	approvalReplyTimeout = 300 * time.Millisecond
	t.Cleanup(func() { approvalReplyTimeout = old })

	s := newChannelService(t, Config{})
	defer s.Close()
	ap := &uiApprover{svc: s, allowed: []string{"shell"}}
	start := time.Now()
	d, err := ap.Review(context.Background(), agent.ApprovalRequest{ToolName: "shell", Arguments: `{"command":"echo hi"}`})
	if err != nil {
		t.Fatal(err)
	}
	if d.Approved {
		t.Fatal("超时必须按拒绝处理（绝不放行）")
	}
	if !strings.Contains(d.Reason, "超时") {
		t.Fatalf("拒绝原因应说明超时：%q", d.Reason)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("应约 300ms 返回，实际 %v", time.Since(start))
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
	if got != normalizeWorkspace(dir) {
		t.Fatalf("规范化后应与 normalizeWorkspace 一致：%q vs %q", got, normalizeWorkspace(dir))
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
