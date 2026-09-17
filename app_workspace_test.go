package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApp_SetAndGetWorkspace(t *testing.T) {
	app := NewApp()
	initWd := app.GetWorkspace()
	if initWd == "" {
		t.Fatalf("expected non-empty initial workspace")
	}

	// 创建临时测试目录
	tmpDir, err := os.MkdirTemp("", "tcode_test_workspace_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 切换到临时目录
	err = app.SetWorkspace(tmpDir)
	if err != nil {
		t.Fatalf("SetWorkspace failed: %v", err)
	}

	gotWd := app.GetWorkspace()
	expectedWd, _ := filepath.Abs(tmpDir)
	expectedWd = filepath.ToSlash(expectedWd)
	if gotWd != expectedWd {
		t.Errorf("GetWorkspace() = %s, expected %s", gotWd, expectedWd)
	}

	// 测试设置非法路径
	err = app.SetWorkspace("")
	if err == nil {
		t.Errorf("expected error when setting empty workspace, got nil")
	}

	nonExistent := filepath.Join(tmpDir, "does_not_exist_sub_dir")
	err = app.SetWorkspace(nonExistent)
	if err == nil {
		t.Errorf("expected error when setting non-existent workspace, got nil")
	}
}

func TestApp_SuggestCommitMessage_FromGitStatus(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tcode_suggest_commit_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	run("init")
	run("config", "user.name", "t")
	run("config", "user.email", "t@t.t")
	if err := os.WriteFile(filepath.Join(tmpDir, "hello.go"), []byte("package h\n"), 0644); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	if err := app.SetWorkspace(tmpDir); err != nil {
		t.Fatal(err)
	}
	msg, err := app.SuggestCommitMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "hello.go") {
		t.Fatalf("expected filename in message, got %q", msg)
	}
}

func TestApp_GitPull_NotARepo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tcode_pull_norepo_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	app := NewApp()
	if err := app.SetWorkspace(tmpDir); err != nil {
		t.Fatal(err)
	}
	_, err = app.GitPull()
	if err == nil {
		t.Fatal("expected git pull to fail outside a repo")
	}
}

func TestApp_GetUsageMetrics_NilSessionStore(t *testing.T) {
	app := &App{
		sessionStore: nil,
	}
	// 验证 sessionStore 为 nil 时不会 panic
	metrics := app.GetUsageMetrics()
	if metrics.ActiveSessions != 0 {
		t.Errorf("expected 0 active sessions when sessionStore is nil, got %d", metrics.ActiveSessions)
	}
}

func TestApp_GitStagingAndCommit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tcode_git_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command failed: %s %v: %s", name, args, string(out))
		}
	}
	runCmd("git", "init")
	runCmd("git", "config", "user.name", "testuser")
	runCmd("git", "config", "user.email", "test@test.com")

	fileA := filepath.Join(tmpDir, "fileA.txt")
	fileB := filepath.Join(tmpDir, "fileB.txt")
	if err := os.WriteFile(fileA, []byte("hello A\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("hello B\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd("git", "add", "-A")
	runCmd("git", "commit", "-m", "init commit")

	app := NewApp()
	if err := app.SetWorkspace(tmpDir); err != nil {
		t.Fatalf("SetWorkspace failed: %v", err)
	}

	// Modify fileA and fileB
	if err := os.WriteFile(fileA, []byte("modified A\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("modified B\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Stage fileA using GitStage
	if err := app.GitStage("fileA.txt"); err != nil {
		t.Fatalf("GitStage fileA failed: %v", err)
	}

	// 2. Unstage fileA using GitUnstage, verify diff --cached is empty
	if err := app.GitUnstage("fileA.txt"); err != nil {
		t.Fatalf("GitUnstage fileA failed: %v", err)
	}
	diffCmd := exec.Command("git", "diff", "--cached", "--quiet")
	diffCmd.Dir = tmpDir
	if diffCmd.Run() != nil {
		t.Fatalf("expected staging area to be empty after GitUnstage")
	}

	// 3. Stage fileA again
	if err := app.GitStage("fileA.txt"); err != nil {
		t.Fatalf("GitStage fileA failed: %v", err)
	}

	// 4. Call GitCommit. Since fileA is staged and fileB is not, GitCommit should ONLY commit fileA!
	msg, err := app.GitCommit("feat: commit staged only")
	if err != nil {
		t.Fatalf("GitCommit failed: %v, output: %s", err, msg)
	}

	// Check git status: fileB should STILL be unstaged / modified in working tree!
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = tmpDir
	statusOut, err := statusCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status failed: %v", err)
	}
	statusStr := string(statusOut)
	if !strings.Contains(statusStr, "fileB.txt") {
		t.Errorf("expected fileB.txt to remain modified/unstaged in working tree, got status: %s", statusStr)
	}
	if strings.Contains(statusStr, "fileA.txt") {
		t.Errorf("expected fileA.txt to be committed and clean, got status: %s", statusStr)
	}
}

func TestApp_RevertFile_NoHeadRepo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tcode_no_head_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %s %v failed: %s", name, args, string(out))
		}
	}
	runCmd("git", "init")
	runCmd("git", "config", "user.name", "testuser")
	runCmd("git", "config", "user.email", "test@test.com")

	app := NewApp()
	if err := app.SetWorkspace(tmpDir); err != nil {
		t.Fatalf("SetWorkspace failed: %v", err)
	}

	// 新建文件并暂存 (status = "A  test.txt")
	testFile := filepath.Join(tmpDir, "brand_new.txt")
	if err := os.WriteFile(testFile, []byte("brand new uncommitted\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd("git", "add", "brand_new.txt")

	// 撤销该暂存文件
	if err := app.RevertFile("brand_new.txt"); err != nil {
		t.Fatalf("RevertFile failed in repo without HEAD: %v", err)
	}

	// 验证文件已被清理
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Errorf("expected brand_new.txt to be removed after revert, but it still exists")
	}
}

func TestApp_GitUnstage_NoHeadRepo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tcode_unstage_nohead_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %s %v failed: %s", name, args, string(out))
		}
	}
	runCmd("git", "init")
	runCmd("git", "config", "user.name", "testuser")
	runCmd("git", "config", "user.email", "test@test.com")

	app := NewApp()
	if err := app.SetWorkspace(tmpDir); err != nil {
		t.Fatalf("SetWorkspace failed: %v", err)
	}

	testFile := filepath.Join(tmpDir, "unstage_test.txt")
	if err := os.WriteFile(testFile, []byte("content to unstage\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := app.GitStage("unstage_test.txt"); err != nil {
		t.Fatalf("GitStage failed: %v", err)
	}

	// 此时文件应处于已暂存状态 (A )
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = tmpDir
	out, _ := statusCmd.CombinedOutput()
	if !strings.HasPrefix(string(out), "A") {
		t.Fatalf("expected file to be staged, got: %s", string(out))
	}

	// 取消暂存
	if err := app.GitUnstage("unstage_test.txt"); err != nil {
		t.Fatalf("GitUnstage failed in repo without HEAD: %v", err)
	}

	// 验证：文件应回到未追踪状态 (??)，且物理文件必须依然存在
	statusCmd2 := exec.Command("git", "status", "--porcelain")
	statusCmd2.Dir = tmpDir
	out2, _ := statusCmd2.CombinedOutput()
	if !strings.HasPrefix(string(out2), "??") {
		t.Errorf("expected file to be untracked (??) after unstage, got: %s", string(out2))
	}
	if _, err := os.Stat(testFile); err != nil {
		t.Errorf("expected physical file to still exist after unstage, got err: %v", err)
	}
}

func TestApp_GitStage_TrailingSlashAndNestedRepo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tcode_stage_robust_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %s %v failed: %s", name, args, string(out))
		}
	}
	runCmd("git", "init")
	runCmd("git", "config", "user.name", "testuser")
	runCmd("git", "config", "user.email", "test@test.com")

	app := NewApp()
	if err := app.SetWorkspace(tmpDir); err != nil {
		t.Fatalf("SetWorkspace failed: %v", err)
	}

	// 1. 空路径校验
	if err := app.GitStage("   "); err == nil {
		t.Errorf("expected error for empty file path in GitStage, got nil")
	}

	// 2. 带尾部斜杠的合法文件
	normalFile := filepath.Join(tmpDir, "robust_test.txt")
	if err := os.WriteFile(normalFile, []byte("robust file content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := app.GitStage("robust_test.txt/ "); err != nil {
		t.Fatalf("GitStage failed on normal file with trailing slash: %v", err)
	}

	// 3. 嵌入式空 Git 仓库
	subDir := filepath.Join(tmpDir, "empty_embedded_repo")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	subCmd := exec.Command("git", "init")
	subCmd.Dir = subDir
	if err := subCmd.Run(); err != nil {
		t.Fatalf("failed to init embedded repo: %v", err)
	}

	// 期望报错为明确的无 commit 提示，而非致命崩溃
	err = app.GitStage("empty_embedded_repo/")
	if err == nil {
		t.Errorf("expected GitStage on empty embedded repo to return error, got nil")
	} else if !strings.Contains(err.Error(), "no commit") {
		t.Errorf("expected error to mention 'no commit', got: %v", err)
	}
}

func TestApp_SearchWorkspace(t *testing.T) {
	// 创建第 1 个临时工作区并写入深层文件
	tmp1, err := os.MkdirTemp("", "tcode_search_ws1_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp1)

	deepDir := filepath.Join(tmp1, "internal", "deep")
	if err := os.MkdirAll(deepDir, 0755); err != nil {
		t.Fatal(err)
	}
	deepFile := filepath.Join(deepDir, "secret_module.go")
	content := "package deep\n\n// MagicTokenForSearchTest\nfunc Secret() string { return \"ok\" }\n"
	if err := os.WriteFile(deepFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.SetWorkspace(tmp1); err != nil {
		t.Fatalf("SetWorkspace tmp1 failed: %v", err)
	}

	// 1. 测试 find：直接检索深层未展开文件名
	findRes, err := app.SearchWorkspace("find", "secret_module.go", 10)
	if err != nil {
		t.Fatalf("SearchWorkspace find failed: %v", err)
	}
	if len(findRes) == 0 {
		t.Fatalf("expected find to return at least 1 match, got 0")
	}
	foundPath := filepath.ToSlash(findRes[0].Path)
	if !strings.Contains(foundPath, "secret_module.go") {
		t.Errorf("expected found path to contain secret_module.go, got: %s", foundPath)
	}

	// 2. 测试 grep：内容检索，必须返回行号与行内容
	grepRes, err := app.SearchWorkspace("grep", "MagicTokenForSearchTest", 10)
	if err != nil {
		t.Fatalf("SearchWorkspace grep failed: %v", err)
	}
	if len(grepRes) == 0 {
		t.Fatalf("expected grep to return at least 1 match, got 0")
	}
	if grepRes[0].Line != 3 {
		t.Errorf("expected match at line 3, got: %d", grepRes[0].Line)
	}
	if !strings.Contains(grepRes[0].Content, "MagicTokenForSearchTest") {
		t.Errorf("expected match content to contain MagicTokenForSearchTest, got: %s", grepRes[0].Content)
	}

	// 3. 测试切换工作区后 search 算子热重载
	tmp2, err := os.MkdirTemp("", "tcode_search_ws2_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp2)

	if err := app.SetWorkspace(tmp2); err != nil {
		t.Fatalf("SetWorkspace tmp2 failed: %v", err)
	}

	// tmp2 中不应搜出 tmp1 的文件
	findRes2, err := app.SearchWorkspace("find", "secret_module.go", 10)
	if err != nil {
		t.Fatalf("SearchWorkspace find on tmp2 failed: %v", err)
	}
	if len(findRes2) != 0 {
		t.Errorf("expected 0 matches for secret_module.go in tmp2, got: %d", len(findRes2))
	}
}



