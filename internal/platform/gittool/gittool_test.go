package gittool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func args(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// newRepo 初始化临时 git 仓库；git 不可用时跳过。
func newRepo(t *testing.T) *Tool {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init")
	return New(dir)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// status：未跟踪文件出现在变更列表。
func TestGitTool_StatusShowsChanges(t *testing.T) {
	tool := newRepo(t)
	if err := os.WriteFile(filepath.Join(tool.root, "new.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "status"}))
	if err != nil || res.IsError {
		t.Fatalf("status failed: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "new.txt") {
		t.Fatalf("status output = %q, want contains new.txt", res.Content)
	}
}

// log：无提交的仓库 → 业务失败（IsError），不是机制 error。
func TestGitTool_LogOnEmptyRepoIsBusinessError(t *testing.T) {
	tool := newRepo(t)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "log"}))
	if err != nil {
		t.Fatalf("must not be mechanism error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("log on empty repo should be business error: %+v", res)
	}
}

// 未知动作 → 业务失败并提示合法取值。
func TestGitTool_UnknownAction(t *testing.T) {
	tool := newRepo(t)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "push"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "status/diff/log") {
		t.Fatalf("result = %+v, want business error listing valid actions", res)
	}
}
