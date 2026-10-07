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

func writeRepoFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

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

// 0.0.11：CurrentBranch 只读取分支名；不是仓库的普通目录返回空串而不是错误
// （界面据此"不显示"，绝不编造分支名）。
func TestCurrentBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	runGit(t, repo, "init")
	// 还没有首个提交的"未出生"分支也要有名字（界面上刚 init 的仓库不应显示为无分支）
	runGit(t, repo, "checkout", "-b", "feature-x")
	if got := CurrentBranch(repo); got != "feature-x" {
		t.Fatalf("CurrentBranch = %q, want feature-x", got)
	}
	if got := CurrentBranch(t.TempDir()); got != "" {
		t.Fatalf("非仓库目录必须返回空串，得到 %q", got)
	}
}

// status -b（0.0.11）：输出带 "## 分支" 头行——顶栏分支显示与模型的分支感知同源。
func TestGitTool_StatusShowsBranchHeader(t *testing.T) {
	tool := newRepo(t)
	runGit(t, tool.root, "checkout", "-b", "feature-y")
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "status"}))
	if err != nil || res.IsError {
		t.Fatalf("status failed: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "feature-y") {
		t.Fatalf("status output = %q, want contains branch header", res.Content)
	}
}

// 模型看的 diff 必须含已暂存块，并在无 path 时列出未跟踪文件。
// Diff()（给提交说明以外的裸未暂存视图）保持原口径，不因这条改动变宽。
func TestGitTool_ModelDiffIncludesStagedAndUntracked(t *testing.T) {
	tool := newRepo(t)
	writeRepoFile(t, tool.root, "a.go", "package a\n")
	runGit(t, tool.root, "add", "a.go")
	runGit(t, tool.root, "commit", "-qm", "init")
	writeRepoFile(t, tool.root, "a.go", "package a\n// STAGED_MARKER\n")
	runGit(t, tool.root, "add", "a.go")
	writeRepoFile(t, tool.root, "new.txt", "u")

	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "diff"}))
	if err != nil || res.IsError {
		t.Fatalf("diff failed: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "STAGED_MARKER") {
		t.Fatalf("已暂存内容必须出现在模型 diff 里：%s", res.Content)
	}
	if !strings.Contains(res.Content, "含已暂存") || !strings.Contains(res.Content, "不含未跟踪") {
		t.Fatalf("口径行缺失：%s", res.Content)
	}
	if !strings.Contains(res.Content, "new.txt") || !strings.Contains(res.Content, "未跟踪文件") {
		t.Fatalf("未跟踪清单缺失：%s", res.Content)
	}

	limited, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "diff", "path": "a.go"}))
	if err != nil || limited.IsError {
		t.Fatalf("path diff failed: %v %s", err, limited.Content)
	}
	if !strings.Contains(limited.Content, "STAGED_MARKER") {
		t.Fatalf("path 限定仍应含已暂存：%s", limited.Content)
	}
	if strings.Contains(limited.Content, "未跟踪文件（不在上面的 diff 里）") || strings.Contains(limited.Content, "new.txt") {
		t.Fatalf("有 path 时不应附未跟踪清单：%s", limited.Content)
	}

	plain, err := Diff(tool.root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "STAGED_MARKER") {
		t.Fatalf("Diff() 仍应只含未暂存，不应含已暂存：%s", plain)
	}
}

func TestGitTool_ModelDiffBeforeFirstCommit(t *testing.T) {
	tool := newRepo(t)
	writeRepoFile(t, tool.root, "a.go", "package a\n")
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "diff"}))
	if err != nil || res.IsError {
		t.Fatalf("无提交时 diff 不应失败：%v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "还没有提交") {
		t.Fatalf("应写明无法相对 HEAD：%s", res.Content)
	}
	if !strings.Contains(res.Content, "a.go") {
		t.Fatalf("应列出未跟踪文件：%s", res.Content)
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
