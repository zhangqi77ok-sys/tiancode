// 工作区快捷命令测试（0.0.26）：三槽往返 + 槽位/空命令校验。
package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuickCommands_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	old := workspaceSettingsPath
	workspaceSettingsPath = func() string { return filepath.Join(dir, "ws.json") }
	defer func() { workspaceSettingsPath = old }()

	root := "D:\\proj-quick"
	if err := saveWorkspaceSettings(root, WorkspaceSettings{
		QuickCommands: QuickCommands{Build: " go build ./... ", Test: "go test ./...", Run: "go run ."},
	}); err != nil {
		t.Fatal(err)
	}
	got := loadWorkspaceSettings(root).QuickCommands
	if got.Build != "go build ./..." {
		t.Fatalf("首尾空白未修剪：%q", got.Build)
	}
	if got.Test != "go test ./..." || got.Run != "go run ." {
		t.Fatalf("三槽往返错：%+v", got)
	}
	// 只配一个槽也算有效设置（不删键）
	if err := saveWorkspaceSettings(root, WorkspaceSettings{QuickCommands: QuickCommands{Test: "go test ./..."}}); err != nil {
		t.Fatal(err)
	}
	if got := loadWorkspaceSettings(root).QuickCommands; got.Build != "" || got.Test != "go test ./..." {
		t.Fatalf("部分槽位保存错：%+v", got)
	}
	// 全空 → 删键
	if err := saveWorkspaceSettings(root, WorkspaceSettings{}); err != nil {
		t.Fatal(err)
	}
	if got := loadWorkspaceSettings(root); !got.QuickCommands.Empty() {
		t.Fatalf("全空后应删键：%+v", got)
	}
}

// 槽位校验与空命令拒绝在编排层（可测）；执行路径与 RunUserCommand 同一条。
func TestRunQuickCommand_RejectsBadInput(t *testing.T) {
	s, ws := newMiniService(t)
	seedSession(t, s, "s-qc", ws)

	// 非法槽位
	if _, err := s.RunQuickCommand(context.Background(), "s-qc", "deploy", "echo x"); err == nil {
		t.Fatal("非法槽位必须拒绝")
	}
	// 空命令（槽位没配）
	if _, err := s.RunQuickCommand(context.Background(), "s-qc", "build", ""); err == nil ||
		!strings.Contains(err.Error(), "尚未配置") {
		t.Fatalf("空命令必须被明确拒绝，got %v", err)
	}
	// 未配置时读槽位：全空
	if q := s.QuickCommandsOf("s-qc"); !q.Empty() {
		t.Fatalf("未配置时三槽应为空：%+v", q)
	}
}

// 已配置的槽位：执行走的是与命令行同一条路（这里用纯对话无工作区的会话验证
// "无工作区"这条显式失败——它证明快捷命令没有绕过工作区校验另开执行器）。
func TestRunQuickCommand_UsesSamePathAsUserCommand(t *testing.T) {
	s, _ := newMiniService(t) // 会话不 seed → 没有 workspace 事件 → 纯对话
	if _, err := s.ledgerFor("s-qc2"); err != nil {
		t.Fatal(err)
	}
	// 直接用会话 ID（无归属 → 纯对话）
	_, err := s.RunQuickCommand(context.Background(), "s-qc2", "test", "echo hi")
	if err == nil || !strings.Contains(err.Error(), "没有工作区") {
		t.Fatalf("纯对话会话应显式拒绝执行（与命令行同规则），got %v", err)
	}
}
