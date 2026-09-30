package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/session"
)

// 第 8 批：检查命令——空命令不产生进程、位置收集与界面/附注同源。
func TestWorkspaceCheck(t *testing.T) {
	// 解析：编译器/测试输出里的 path:line 与 path:line:col 都收；说明文字里的编号不收
	refs := ParseCheckRefs(strings.Join([]string{
		"--- FAIL: TestX",
		"foo.go:10: undefined: bar",
		"internal/app/x.go:7:3: syntax error",
		"note:12:3 这说的是注释里的编号",
		"ok  \ttiancode/internal/app\t0.9s",
	}, "\n"))
	if len(refs) != 2 {
		t.Fatalf("refs = %+v, want 2", refs)
	}
	if refs[0].Path != "foo.go" || refs[0].Line != 10 {
		t.Fatalf("第一个引用 = %+v", refs[0])
	}
	if refs[1].Path != "internal/app/x.go" || refs[1].Line != 7 || refs[1].Col != 3 {
		t.Fatalf("第二个引用 = %+v", refs[1])
	}
}

func TestRunWorkspaceCheck_EmptyNeverRuns(t *testing.T) {
	dir := t.TempDir()
	oldPath := workspaceSettingsPath
	workspaceSettingsPath = func() string { return filepath.Join(dir, "workspace-settings.json") }
	defer func() { workspaceSettingsPath = oldPath }()

	s := newChannelService(t, Config{})
	defer s.Close()
	root := t.TempDir()
	l, err := s.ledgerFor("s-check")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": root}); err != nil {
		t.Fatal(err)
	}

	called := 0
	oldRun := checkRunner
	checkRunner = func(_ context.Context, name string, args []string, dir string) (string, error) {
		called++
		return "foo.go:10: boom", nil
	}
	defer func() { checkRunner = oldRun }()

	// 未配置：不产生任何进程
	res, err := s.RunWorkspaceCheck("s-check")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped || called != 0 {
		t.Fatalf("空命令必须跳过且不跑进程：skipped=%v called=%d", res.Skipped, called)
	}
	if note := s.checkNote("s-check"); note != "" {
		t.Fatalf("没有结果时不该有附注：%q", note)
	}

	// 配置后：按空格拆 argv、工作目录是这场对话的工作区
	if err := saveWorkspaceSettings(root, WorkspaceSettings{CheckCommand: "go test ./..."}); err != nil {
		t.Fatal(err)
	}
	var gotName, gotArgs, gotDir string
	checkRunner = func(_ context.Context, name string, args []string, wd string) (string, error) {
		called++
		gotName, gotArgs, gotDir = name, strings.Join(args, " "), wd
		return "foo.go:10: undefined: bar", nil
	}
	res, err = s.RunWorkspaceCheck("s-check")
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped || called != 1 {
		t.Fatalf("配置后应跑一次：skipped=%v called=%d", res.Skipped, called)
	}
	if gotName != "go" || gotArgs != "test ./..." || gotDir != root {
		t.Fatalf("argv/dir = %q %q %q", gotName, gotArgs, gotDir)
	}
	if len(res.Refs) != 1 || res.Refs[0].Path != "foo.go" {
		t.Fatalf("refs = %+v", res.Refs)
	}
	// 下一轮附注：标题写明不是用户原话，正文与列表一致
	note := s.checkNote("s-check")
	if !strings.Contains(note, "工作区检查，不是用户原话") || !strings.Contains(note, "foo.go:10") {
		t.Fatalf("附注 = %q", note)
	}
}
