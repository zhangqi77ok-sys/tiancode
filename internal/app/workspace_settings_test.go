package app

import (
	"path/filepath"
	"strings"
	"testing"
)

// 第 8 批：工作区设置的 argv 规则——按空格拆分、替换 {path}/{line}、不经 shell。
func TestOpenAtLineArgv(t *testing.T) {
	// 未配置：交给调用方退回 OpenInDefaultApp
	if _, _, ok := OpenAtLineArgv("", "/w/a.go", 120); ok {
		t.Fatal("空模板不得走这条命令")
	}
	// 行号缺失 / 0：同样退回
	if _, _, ok := OpenAtLineArgv("code -g {path}:{line}", "/w/a.go", 0); ok {
		t.Fatal("行号 0 不得调用命令")
	}
	name, args, ok := OpenAtLineArgv("code -g {path}:{line}", "/w/a.go", 120)
	if !ok {
		t.Fatal("配置齐全应走这条命令")
	}
	if name != "code" || strings.Join(args, " ") != "-g /w/a.go:120" {
		t.Fatalf("argv = %q %v", name, args)
	}
	// 路径含空格：不自行加引号，也不把路径再拆开——先按空格拆模板，再做整体替换，
	// 于是 "/w/my dir/a.go" 稳稳落在同一个 argv 元素里（契约就这一条拆法）。
	name, args, _ = OpenAtLineArgv("myed {path} {line}", "/w/my dir/a.go", 7)
	if name != "myed" || len(args) != 2 || args[0] != "/w/my dir/a.go" || args[1] != "7" {
		t.Fatalf("空格路径不该被拆成多个 argv 元素：%q %v", name, args)
	}
}

// 未配置时的退回路径必须与 OpenInDefaultApp 完全一致（同一个 argv 构造）。
func TestDefaultOpenArgv(t *testing.T) {
	name, args := DefaultOpenArgv(`C:\w\a.go`, false)
	if name != "cmd" || strings.Join(args, " ") != `/c start  C:\w\a.go` {
		t.Fatalf("文件应按关联程序打开：%q %v", name, args)
	}
	name, args = DefaultOpenArgv(`C:\w`, true)
	if name != "explorer" || len(args) != 1 {
		t.Fatalf("目录应直接开目录：%q %v", name, args)
	}
}

// 存储：按工作区绝对路径作 key，两台工作区互不覆盖；清空 = 删键。
func TestWorkspaceSettingsStore(t *testing.T) {
	dir := t.TempDir()
	old := workspaceSettingsPath
	workspaceSettingsPath = func() string { return filepath.Join(dir, "workspace-settings.json") }
	defer func() { workspaceSettingsPath = old }()

	a, b := filepath.Join(t.TempDir(), "proj-a"), filepath.Join(t.TempDir(), "proj-b")
	if err := saveWorkspaceSettings(a, WorkspaceSettings{OpenAtLine: "code -g {path}:{line}"}); err != nil {
		t.Fatal(err)
	}
	if err := saveWorkspaceSettings(b, WorkspaceSettings{CheckCommand: "go test ./..."}); err != nil {
		t.Fatal(err)
	}
	if got := loadWorkspaceSettings(a).OpenAtLine; got != "code -g {path}:{line}" {
		t.Fatalf("A 的设置 = %q", got)
	}
	if got := loadWorkspaceSettings(b).CheckCommand; got != "go test ./..." {
		t.Fatalf("B 的设置 = %q", got)
	}
	if got := loadWorkspaceSettings(a).CheckCommand; got != "" {
		t.Fatalf("A 不该有检查命令：%q", got)
	}
	// 大小写 / 分隔符差异不该当成另一个工作区（Windows 语义）
	if got := loadWorkspaceSettings(strings.ToUpper(a)).OpenAtLine; got == "" {
		t.Fatal("同一目录的另一种写法应命中同一条目")
	}
	// 清空 = 删键
	if err := saveWorkspaceSettings(a, WorkspaceSettings{}); err != nil {
		t.Fatal(err)
	}
	if got := loadWorkspaceSettings(a).OpenAtLine; got != "" {
		t.Fatalf("清空后应为零值：%q", got)
	}
	// 缺文件 = 零值（不报错）；含切片字段后不可整结构体比较，逐字段判
	if got := loadWorkspaceSettings(filepath.Join(dir, "nope")); got.OpenAtLine != "" || got.CheckCommand != "" || len(got.ShellAllow) != 0 {
		t.Fatalf("缺文件应为零值：%+v", got)
	}
}
