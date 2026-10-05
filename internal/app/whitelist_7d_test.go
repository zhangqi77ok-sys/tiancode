// 审批白名单（0.0.24）的用例测试。
package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tiancode/internal/core/agent"
)

type stubApprover struct {
	fn func(context.Context, agent.ApprovalRequest) (agent.Decision, error)
}

func (s stubApprover) Review(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
	return s.fn(ctx, req)
}

// shellAllowApprover：命中前缀自动放行；shell 未命中与非 shell 原样交内层。
func TestShellAllowApprover(t *testing.T) {
	innerCalls := 0
	inner := stubApprover{fn: func(ctx context.Context, req agent.ApprovalRequest) (agent.Decision, error) {
		innerCalls++
		return agent.Decision{Approved: false, Reason: "ui"}, nil
	}}
	a := &shellAllowApprover{inner: inner, allow: []string{"git ", "go test"}}

	res, err := a.Review(context.Background(), agent.ApprovalRequest{
		ToolName: "shell", Arguments: `{"command":"git status --porcelain"}`,
	})
	if err != nil || !res.Approved {
		t.Fatalf("命中前缀应自动放行：%+v %v", res, err)
	}
	if innerCalls != 0 {
		t.Fatalf("命中前缀不应再问 UI（innerCalls=%d）", innerCalls)
	}

	res, _ = a.Review(context.Background(), agent.ApprovalRequest{
		ToolName: "shell", Arguments: `{"command":"rm -rf /"}`,
	})
	if res.Approved || innerCalls != 1 {
		t.Fatalf("未命中必须交内层（UI 确认）：%+v innerCalls=%d", res, innerCalls)
	}

	res, _ = a.Review(context.Background(), agent.ApprovalRequest{
		ToolName: "fs", Arguments: `{"path":"x"}`,
	})
	if res.Approved || innerCalls != 2 {
		t.Fatalf("非 shell 不享受白名单：%+v innerCalls=%d", res, innerCalls)
	}
}

// 0.0.25 加固：组合/重定向/求值记号与词边界——白名单只放行"干净的"前缀命令。
//
// 期望值口径（与实现的语义对齐，勿再改回去）：
//   - 返回的 pre 是**修剪后的**前缀（配置里写 "git "、返回 "git"）——它只用于
//     审批通过时的留痕文案（"命中白名单（前缀 git）"），展示用户意图即可；
//   - 用户**显式配了**的前缀必命中（allow 里有 "gitx"，"gitx status" 就该过）——
//     "gitx 不吃 git 前缀"说的是词边界，与"gitx 自己在名单里"是两件事。
func TestShellAllowMatched_Hardened(t *testing.T) {
	allow := []string{"git ", "go test", "gitx"}
	cases := []struct {
		cmd  string
		want bool
		pre  string
	}{
		{"git status --porcelain", true, "git"},
		{"go test ./...", true, "go test"},
		{"git", true, "git"},                  // 前缀即全命令
		{"gitx status", true, "gitx"},         // 显式配了 gitx：命中（词边界指的是 git 不吃 gitx）
		{"git status; rm -rf x", false, ""},   // 组合命令
		{"git status && del x", false, ""},    // 组合命令
		{"git pull || evil", false, ""},       // 组合命令
		{"git log | findstr x", false, ""},    // 管道
		{"git log > C:\\evil.dll", false, ""}, // 重定向写文件
		{"git commit -m \"a\nb\"", false, ""}, // 换行
		{"echo $(evil)", false, ""},           // 求值（虽是 bash 语法，一并拦）
		{"echo %PATH%", false, ""},            // cmd %VAR% 环境变量展开（0.0.26）
		{"git log --format=%h", false, ""},    // % 误伤合法用法时转走审批，安全方向
		{"  git status", true, "git"},         // 首尾空白容忍
		{"gitx", true, "gitx"},                // 恰好等于显式前缀
	}
	for _, c := range cases {
		pre, ok := shellAllowMatched(c.cmd, allow)
		if ok != c.want || (ok && pre != c.pre) {
			t.Fatalf("shellAllowMatched(%q) = (%q, %v), want (%q, %v)", c.cmd, pre, ok, c.pre, c.want)
		}
	}
	if _, ok := shellAllowMatched("", allow); ok {
		t.Fatal("空命令不命中")
	}
	if _, ok := shellAllowMatched("git status", []string{""}); ok {
		t.Fatal("空前缀不命中")
	}
}

// approverFor：工作区设置配了 ShellAllow 时套上旁路审批器；没配时不套。
func TestApproverFor_UsesWorkspaceShellAllow(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	root := t.TempDir()
	// 设置文件指到临时目录：绝不读写用户真实的 workspace-settings.json。
	// 目录必须算一次存进变量——闭包里直接调 t.TempDir() 每次都会生成新目录，
	// 存和读就会落到两个文件（实测踩过）。
	settingsDir := t.TempDir()
	old := workspaceSettingsPath
	workspaceSettingsPath = func() string { return filepath.Join(settingsDir, "workspace-settings.json") }
	defer func() { workspaceSettingsPath = old }()

	s.approvalTools = []string{"shell", "ext_manage"}
	a := s.approverFor("s-wl", root)
	if _, ok := a.(*shellAllowApprover); ok {
		t.Fatal("没配白名单不应套旁路审批器")
	}

	// 直接落工作区设置文件（包内函数，测试隔离由 t.TempDir 保证）
	if err := saveWorkspaceSettings(root, WorkspaceSettings{ShellAllow: []string{"git "}}); err != nil {
		t.Fatal(err)
	}
	ws := loadWorkspaceSettings(root)
	t.Logf("loaded after save: %+v", ws)
	if raw, err := os.ReadFile(workspaceSettingsPath()); err == nil {
		t.Logf("raw file: %s", raw)
	} else {
		t.Logf("raw read err: %v", err)
	}
	a = s.approverFor("s-wl", root)
	if _, ok := a.(*shellAllowApprover); !ok {
		t.Fatalf("配了白名单必须套旁路审批器：%T", a)
	}
}
