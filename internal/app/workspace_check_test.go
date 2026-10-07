package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/llm"
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

func TestParseCheckRefs_DriveAndParen(t *testing.T) {
	refs := ParseCheckRefs("D:\\a\\f.go:3:1: x\nApp.vue(12,5): error TS2322: type\nnote:12:3 这说的是注释里的编号\n")
	if len(refs) != 2 {
		t.Fatalf("refs = %+v", refs)
	}
	if refs[0].Path != `D:\a\f.go` || refs[0].Line != 3 || refs[0].Col != 1 || refs[0].Text != "x" {
		t.Fatalf("drive ref = %+v", refs[0])
	}
	if refs[1].Path != "App.vue" || refs[1].Line != 12 || refs[1].Col != 5 || !strings.Contains(refs[1].Text, "TS2322") {
		t.Fatalf("paren ref = %+v", refs[1])
	}
}

func TestCheckNote_FailedWithoutRefsDoesNotAutoFix(t *testing.T) {
	s, root := newCheckFixture(t, "s-noref")
	if err := saveWorkspaceSettings(root, WorkspaceSettings{CheckCommand: "go test ./..."}); err != nil {
		t.Fatal(err)
	}
	old := checkRunner
	t.Cleanup(func() { checkRunner = old })
	checkRunner = func(context.Context, string, []string, string) (string, error) {
		return "FAIL\nno locations\n", errStub{}
	}
	if _, err := s.RunWorkspaceCheck("s-noref"); err != nil {
		t.Fatal(err)
	}
	note := s.checkNote("s-noref")
	if note == "" || !strings.Contains(note, "未能解析出位置") || strings.Contains(note, "自动修复") {
		t.Fatalf("附注 = %q", note)
	}
	if !strings.Contains(note, "FAIL") {
		t.Fatalf("原文应在附注里：%q", note)
	}

	calls := 0
	s.SetAutoFixHandler(func(string, string, int, int) { calls++ })
	s.afterTurnCheck("s-noref", llm.EndDone)
	if calls != 0 {
		t.Fatalf("没有位置引用不得启动自愈，calls=%d", calls)
	}
}

func TestCheckNote_FailedWithoutRefsKeepsHeadAndTail(t *testing.T) {
	s, root := newCheckFixture(t, "s-big")
	if err := saveWorkspaceSettings(root, WorkspaceSettings{CheckCommand: "go test ./..."}); err != nil {
		t.Fatal(err)
	}
	old := checkRunner
	t.Cleanup(func() { checkRunner = old })
	body := "HEADMARKER\n" + strings.Repeat("x", 20*1024) + "\nTAILMARKER"
	checkRunner = func(context.Context, string, []string, string) (string, error) {
		return body, errStub{}
	}
	if _, err := s.RunWorkspaceCheck("s-big"); err != nil {
		t.Fatal(err)
	}
	note := s.checkNote("s-big")
	if !strings.Contains(note, "HEADMARKER") || !strings.Contains(note, "TAILMARKER") || !strings.Contains(note, "未能解析出位置") {
		t.Fatalf("头尾应保留：%s", note)
	}
	if len(note) > checkNoteRawLimit+512 {
		t.Fatalf("附注过长：%d", len(note))
	}
}

// newCheckFixture 把检查设置写到临时文件，并把这场对话的工作区绑到另一个临时目录。
// 不改真实的用户设置文件。
func newCheckFixture(t *testing.T, sessionID string) (*ChatService, string) {
	t.Helper()
	dir := t.TempDir()
	oldPath := workspaceSettingsPath
	workspaceSettingsPath = func() string { return filepath.Join(dir, "workspace-settings.json") }
	t.Cleanup(func() { workspaceSettingsPath = oldPath })

	s := newChannelService(t, Config{})
	t.Cleanup(func() { s.Close() })
	root := t.TempDir()
	l, err := s.ledgerFor(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": root}); err != nil {
		t.Fatal(err)
	}
	return s, root
}

// 0.0.43（C-APP-6）：工作区设置未配 CheckCommand 时，回退 AGENTS.md frontmatter
// 的 check 命令；设置显式配置优先。
func TestRunWorkspaceCheck_AgentsFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"),
		[]byte("---\ncheck: fake-check stub\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newChannelService(t, Config{})
	defer s.Close()
	l, err := s.ledgerFor("s-agents-check")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventWorkspace, map[string]string{"path": root}); err != nil {
		t.Fatal(err)
	}
	sess := "s-agents-check"

	old := checkRunner
	checkRunner = func(_ context.Context, name string, args []string, _ string) (string, error) {
		return strings.Join(append([]string{name}, args...), " ") + " OK", nil
	}
	defer func() { checkRunner = old }()

	res, err := s.RunWorkspaceCheck(sess)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped || res.Command != "fake-check stub" {
		t.Fatalf("应回退到 AGENTS.md 声明的命令：%+v", res)
	}

	// 设置显式配置优先于仓库声明
	if err := saveWorkspaceSettings(root, WorkspaceSettings{CheckCommand: "settings-wins"}); err != nil {
		t.Fatal(err)
	}
	res, err = s.RunWorkspaceCheck(sess)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped || res.Command != "settings-wins" {
		t.Fatalf("设置应优先：%+v", res)
	}
}
