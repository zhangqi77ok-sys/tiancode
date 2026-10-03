// 审批白名单（0.0.24）与用量 7 天聚合的用例测试。
package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tiancode/internal/core/agent"
	"tiancode/internal/core/session"
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

// 用量 7 天聚合：带 at 的旧事件不计入、新事件计入、无 at 的不参与。
func TestMeta_Usage7dWindow(t *testing.T) {
	dir := t.TempDir()
	l, err := session.OpenLedger(dir, "s-7d")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	appendUsage := func(at int64) {
		if _, err := l.Append(session.EventUsage, map[string]any{
			"prompt": 10, "completion": 5, "total": 15, "at": at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	appendUsage(time.Now().AddDate(0, 0, -1).UnixMilli())  // 窗口内
	appendUsage(time.Now().AddDate(0, 0, -30).UnixMilli()) // 窗口外
	if _, err := l.Append(session.EventUsage, map[string]any{
		"prompt": 100, "completion": 50, "total": 150, // 无 at：旧版事件
	}); err != nil {
		t.Fatal(err)
	}
	m, err := session.ReadMeta(dir, "s-7d")
	if err != nil {
		t.Fatal(err)
	}
	if m.UsageTotal != 180 || m.UsagePrompt != 120 {
		t.Fatalf("累计应包含全部（含无 at 的）：%+v", m)
	}
	if m.Usage7dTotal != 15 || m.Usage7dPrompt != 10 || m.Usage7dCompletion != 5 {
		t.Fatalf("7 天窗口应只含带 at 且窗口内的事件：%+v", m)
	}
}

// RecordUsage 落的事件必须带 at（否则 7 天窗口永远为空）。
func TestRecordUsage_StampsTimestamp(t *testing.T) {
	s := newChannelService(t, Config{})
	defer s.Close()
	if _, err := s.ledgerFor("s-at"); err != nil {
		t.Fatal(err)
	}
	s.RecordUsage("s-at", 10, 5, 15)
	l, err := s.ledgerFor("s-at")
	if err != nil {
		t.Fatal(err)
	}
	var sawAt bool
	if err := l.Replay(func(ev session.Event) error {
		if ev.Kind() != session.EventUsage {
			return nil
		}
		var p struct {
			At int64 `json:"at"`
		}
		if json.Unmarshal(ev.Data(), &p) == nil && p.At > 0 {
			sawAt = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !sawAt {
		t.Fatal("usage 事件必须带 at 时间戳")
	}
}
