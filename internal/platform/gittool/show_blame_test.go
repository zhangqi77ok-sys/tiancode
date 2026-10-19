package gittool

import (
	"context"
	"strings"
	"testing"
)

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "-m", msg)
}

// 2026-10-19：show 看某次提交改了什么；blame 看行的来历（此前只有 status/diff/log）。
func TestGitTool_ShowAndBlame(t *testing.T) {
	tool := newRepo(t)
	writeRepoFile(t, tool.root, "a.txt", "one\ntwo\n")
	commitAll(t, tool.root, "first commit")
	writeRepoFile(t, tool.root, "a.txt", "one\nTWO\nthree\n")
	commitAll(t, tool.root, "second commit")

	res, err := tool.Execute(context.Background(), args(t, map[string]any{"action": "show", "rev": "HEAD"}))
	if err != nil || res.IsError || !strings.Contains(res.Content, "second commit") || !strings.Contains(res.Content, "+TWO") {
		t.Fatalf("show 应给出说明与补丁：%v %q", err, res.Content)
	}
	res, err = tool.Execute(context.Background(), args(t, map[string]any{"action": "show", "rev": "HEAD~1", "path": "a.txt"}))
	if err != nil || res.IsError || !strings.Contains(res.Content, "first commit") {
		t.Fatalf("show 应支持 HEAD~1 与 path：%v %q", err, res.Content)
	}
	res, err = tool.Execute(context.Background(), args(t, map[string]any{"action": "blame", "path": "a.txt", "start_line": 2, "line_count": 1}))
	if err != nil || res.IsError || !strings.Contains(res.Content, "TWO") || strings.Contains(res.Content, "three") {
		t.Fatalf("blame 应只给第 2 行：%v %q", err, res.Content)
	}
}

// rev 不能被当成选项注入（--output 会写文件）；blame 必须有 path。
func TestGitTool_RevGuards(t *testing.T) {
	for _, bad := range []string{"", "--output=x", "-p", "HEAD;rm", "a b"} {
		if _, err := buildArgsWith("show", "", 0, bad, 0, 0); err == nil {
			t.Fatalf("rev %q 应拒绝", bad)
		}
	}
	for _, ok := range []string{"HEAD", "HEAD~2", "main^", "v1.2.3", "abc123", "origin/main", "HEAD@{1}"} {
		if _, err := buildArgsWith("show", "", 0, ok, 0, 0); err != nil {
			t.Fatalf("rev %q 应接受：%v", ok, err)
		}
	}
	if _, err := buildArgsWith("blame", "", 0, "", 0, 0); err == nil {
		t.Fatal("blame 没有 path 应拒绝")
	}
}
