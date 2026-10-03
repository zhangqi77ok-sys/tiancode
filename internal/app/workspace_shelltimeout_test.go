// 工作区默认 shell 超时（0.0.25）：保存校验 + 解析口径 + 装配生效。
package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestShellDefaultTimeoutOf(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want time.Duration // 0 = 用 shelltool 内置默认
	}{
		{"未配置", 0, 0},
		{"120 秒", 120, 120 * time.Second},
		{"600 秒（硬顶）", 600, 600 * time.Second},
		{"超硬顶夹到 600", 9999, 600 * time.Second},
	}
	for _, tc := range cases {
		got := shellDefaultTimeoutOf(WorkspaceSettings{ShellTimeoutSeconds: tc.in})
		if got != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// 保存：合法值往返；非法值（负数/超顶）显式拒绝且不落盘。
func TestSaveWorkspaceSettings_ShellTimeout(t *testing.T) {
	dir := t.TempDir()
	old := workspaceSettingsPath
	workspaceSettingsPath = func() string { return filepath.Join(dir, "ws.json") }
	defer func() { workspaceSettingsPath = old }()

	root := "D:\\proj-timeout"
	if err := saveWorkspaceSettings(root, WorkspaceSettings{ShellTimeoutSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	if got := loadWorkspaceSettings(root).ShellTimeoutSeconds; got != 300 {
		t.Fatalf("往返丢值：%d", got)
	}
	for _, bad := range []int{-1, 601} {
		if err := saveWorkspaceSettings(root, WorkspaceSettings{ShellTimeoutSeconds: bad}); err == nil {
			t.Fatalf("非法值 %d 必须被拒绝", bad)
		}
	}
	// 拒绝后磁盘仍是上一轮的值（没有半截写入）
	if got := loadWorkspaceSettings(root).ShellTimeoutSeconds; got != 300 {
		t.Fatalf("拒绝后磁盘被改：%d", got)
	}
	// 全空才删键：只留超时也算有效设置
	if err := saveWorkspaceSettings(root, WorkspaceSettings{ShellTimeoutSeconds: 200}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspaceSettingsPath()); err != nil {
		t.Fatalf("仅超时非空时不应删键：%v", err)
	}
	// 清空超时后键应被删除（其余也空）
	if err := saveWorkspaceSettings(root, WorkspaceSettings{}); err != nil {
		t.Fatal(err)
	}
	var all map[string]WorkspaceSettings
	raw, err := os.ReadFile(workspaceSettingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	if _, ok := all["d:/proj-timeout"]; ok {
		t.Fatalf("全空后键未删：%+v", all)
	}
}
