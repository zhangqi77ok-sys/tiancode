package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/agent"
)

// reviewAsync 在后台发起一次审批并返回事件与结果通道。
func reviewAsync(t *testing.T, ap *uiApprover, events chan ApprovalEvent, cmd string) (ApprovalEvent, chan agent.Decision) {
	t.Helper()
	done := make(chan agent.Decision, 1)
	go func() {
		d, err := ap.Review(context.Background(), agent.ApprovalRequest{ToolName: "shell", Arguments: `{"command":"` + cmd + `"}`})
		if err != nil {
			t.Errorf("review err: %v", err)
		}
		done <- d
	}()
	select {
	case ev := <-events:
		return ev, done
	case <-time.After(2 * time.Second):
		t.Fatal("超时未收到审批事件")
	}
	return ApprovalEvent{}, nil
}

// 审批卡「本会话总是允许此前缀」：批准当前命令 + 本会话同前缀免确认；
// 别的会话、不同前缀、带组合记号的命令照旧要确认（2026-10-19：跑测试循环每次都要点一下）。
func TestApproval_SessionAllowPrefix(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	events := make(chan ApprovalEvent, 4)
	s.SetApprovalHandler(func(e ApprovalEvent) { events <- e })
	ap := &uiApprover{svc: s, sessionID: "s-a", allowed: []string{"shell"}}

	ev, done := reviewAsync(t, ap, events, "go test ./...")
	if ev.SuggestedPrefix != "go test" {
		t.Fatalf("建议前缀应为 go test：%q", ev.SuggestedPrefix)
	}
	// 前缀必须覆盖这条命令
	if err := s.ResolveApprovalAllowPrefix(ev.ID, "npm test"); err == nil {
		t.Fatal("不覆盖当前命令的前缀必须拒绝")
	}
	if err := s.ResolveApprovalAllowPrefix(ev.ID, "go test; rm"); err == nil {
		t.Fatal("含组合记号的前缀必须拒绝")
	}
	if err := s.ResolveApprovalAllowPrefix(ev.ID, "go test"); err != nil {
		t.Fatal(err)
	}
	if d := <-done; !d.Approved || !strings.Contains(d.Reason, "go test") {
		t.Fatalf("当前命令应被批准并留痕：%+v", d)
	}

	// 同会话同前缀：直接放行、不再发事件
	d, err := ap.Review(context.Background(), agent.ApprovalRequest{ToolName: "shell", Arguments: `{"command":"go test ./internal/app"}`})
	if err != nil || !d.Approved {
		t.Fatalf("同前缀应免确认：%+v err=%v", d, err)
	}
	select {
	case e := <-events:
		t.Fatalf("免确认时不应发审批事件：%+v", e)
	default:
	}

	// 组合命令即使以前缀开头也要确认（与工作区白名单同一规则）
	ev2, done2 := reviewAsync(t, ap, events, "go test ./... && del x")
	if ev2.SuggestedPrefix != "" {
		t.Fatalf("组合命令不应给建议前缀：%q", ev2.SuggestedPrefix)
	}
	if err := s.ResolveApproval(ev2.ID, false, "no"); err != nil {
		t.Fatal(err)
	}
	<-done2

	// 别的会话不受影响
	other := &uiApprover{svc: s, sessionID: "s-b", allowed: []string{"shell"}}
	ev3, done3 := reviewAsync(t, other, events, "go test ./...")
	if err := s.ResolveApproval(ev3.ID, false, "no"); err != nil {
		t.Fatal(err)
	}
	<-done3

	// 设置面板：查看与撤销（撤销立即生效，下一条同前缀命令重新询问）
	if got := s.SessionShellAllow("s-a"); len(got) != 1 || got[0] != "go test" {
		t.Fatalf("本会话授予列表不符：%v", got)
	}
	if err := s.RevokeSessionShellAllow("s-a", "npm test"); err == nil {
		t.Fatal("撤销不存在的前缀应报错")
	}
	if err := s.RevokeSessionShellAllow("s-a", "go test"); err != nil {
		t.Fatal(err)
	}
	ev4, done4 := reviewAsync(t, ap, events, "go test ./...")
	if err := s.ResolveApproval(ev4.ID, false, "no"); err != nil {
		t.Fatal(err)
	}
	<-done4
	if tools := s.ApprovableTools(); len(tools) == 0 || tools[0] != "shell" {
		t.Fatalf("可审批工具清单不符：%v", tools)
	}
}

// 2026-10-19：界面"开启审批"恢复的清单必须是出厂默认的完整四项（此前前端写死
// [shell, ext_manage]，关→开一次就永久丢掉 mcp/browser 保护）；返回副本。
func TestDefaultApprovalPolicy_MatchesFreshInstall(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	def := s.DefaultApprovalPolicy()
	fresh := s.ApprovalPolicy()
	if strings.Join(def, ",") != strings.Join(fresh, ",") || strings.Join(def, ",") != "shell,ext_manage,mcp,browser" {
		t.Fatalf("默认清单 %v 应等于新装清单 %v（四项）", def, fresh)
	}
	def[0] = "被篡改"
	if s.DefaultApprovalPolicy()[0] != "shell" {
		t.Fatal("DefaultApprovalPolicy 必须返回副本")
	}
	// 关→用默认清单开：四项完整恢复
	if err := s.SetApprovalPolicy(nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetApprovalPolicy(s.DefaultApprovalPolicy()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.ApprovalPolicy(), ","); got != "shell,ext_manage,mcp,browser" {
		t.Fatalf("关→开后应恢复完整默认清单：%s", got)
	}
}

func TestSuggestShellPrefix(t *testing.T) {
	cases := map[string]string{
		"go test ./...":       "go test",
		"npm run build":       "npm run",
		"git status":          "git status",
		"python -m pytest":    "python",
		"node scripts/a.js":   "node",
		"dir":                 "dir",
		"echo a > out.txt":    "",
		"go vet ./... | find": "",
	}
	for cmd, want := range cases {
		if got := suggestShellPrefix(cmd); got != want {
			t.Errorf("suggestShellPrefix(%q) = %q, want %q", cmd, got, want)
		}
	}
}
