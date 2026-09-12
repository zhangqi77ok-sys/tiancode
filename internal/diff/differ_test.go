package diff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputeFileDiff_DetectLanguage(t *testing.T) {
	cases := map[string]string{
		"main.go":       "Go · UTF-8",
		"App.vue":       "Vue SFC · UTF-8",
		"index.ts":      "TypeScript · UTF-8",
		"package.json":  "JSON · UTF-8",
		"README.md":     "Markdown · UTF-8",
		"script.sh":     "Plain Text · UTF-8",
	}

	for f, expected := range cases {
		lang := detectLanguage(f)
		if lang != expected {
			t.Errorf("file %s expected %s, got %s", f, expected, lang)
		}
	}
}

func TestComputeFileDiff_CleanFile(t *testing.T) {
	wd, _ := os.Getwd()
	// app.go 在 git 仓库根目录下
	report, err := ComputeFileDiff(filepath.Dir(filepath.Dir(wd)), "app.go")
	if err != nil {
		return
	}
	if report.FilePath != "app.go" {
		t.Errorf("expected FilePath app.go, got %s", report.FilePath)
	}
}

func TestBuildUnifiedPatch(t *testing.T) {
	relPath := "main.go"
	header := "@@ -10,3 +10,4 @@"
	lines := []string{
		" func main() {",
		"+    println(\"hello\")",
		" }",
	}

	patch := buildUnifiedPatch(relPath, header, lines)
	if !strings.Contains(patch, "--- a/main.go") {
		t.Errorf("patch missing '--- a/main.go'")
	}
	if !strings.Contains(patch, "+++ b/main.go") {
		t.Errorf("patch missing '+++ b/main.go'")
	}
	if !strings.Contains(patch, "+    println(\"hello\")") {
		t.Errorf("patch missing added line")
	}
}

func TestComputeFileDiff_UntrackedFile(t *testing.T) {
	wd, _ := os.Getwd()
	repoRoot := filepath.Dir(filepath.Dir(wd))

	tempFileName := "temp_untracked_test_file.txt"
	absPath := filepath.Join(repoRoot, tempFileName)
	_ = os.WriteFile(absPath, []byte("line1\nline2\nline3"), 0644)
	defer os.Remove(absPath)

	report, err := ComputeFileDiff(repoRoot, tempFileName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(report.Lines))
	}
	if len(report.Hunks) != 1 {
		t.Errorf("expected 1 hunk, got %d", len(report.Hunks))
	}
	for i, l := range report.Lines {
		if l.Type != "add" {
			t.Errorf("line %d expected type add, got %s", i, l.Type)
		}
	}
	if !strings.Contains(report.Stats, "新文件") {
		t.Errorf("expected stats to contain 新文件, got %s", report.Stats)
	}
}

func TestComputeFileDiff_PathTraversal(t *testing.T) {
	wd, _ := os.Getwd()
	repoRoot := filepath.Dir(filepath.Dir(wd))

	maliciousPaths := []string{
		"../outside.txt",
		"../../windows/system32/cmd.exe",
		"..\\outside.txt",
		"",
	}

	for _, p := range maliciousPaths {
		_, err := ComputeFileDiff(repoRoot, p)
		if err == nil {
			t.Errorf("expected path traversal error for [%s], but got nil", p)
		}
	}
}

func TestValidateRelPath_LeadingSlash(t *testing.T) {
	wd, _ := os.Getwd()
	repoRoot := filepath.Dir(filepath.Dir(wd))

	// 带前导斜杠的文件路径应当能正常通过校验，不报逃逸
	p, err := validateRelPath(repoRoot, "/app.go")
	if err != nil {
		t.Fatalf("expected /app.go to pass validation, got err: %v", err)
	}
	expected := filepath.Join(repoRoot, "app.go")
	if !strings.EqualFold(p, expected) {
		t.Errorf("expected %s, got %s", expected, p)
	}
}

func TestDiscardHunkPatch_UntrackedFile(t *testing.T) {
	wd, _ := os.Getwd()
	repoRoot := filepath.Dir(filepath.Dir(wd))

	tempFileName := "temp_untracked_discard_test.txt"
	absPath := filepath.Join(repoRoot, tempFileName)
	_ = os.WriteFile(absPath, []byte("discard this untracked line 1\nline 2"), 0644)
	defer os.Remove(absPath)

	err := DiscardHunkPatch(repoRoot, tempFileName, 0)
	if err != nil {
		t.Fatalf("DiscardHunkPatch failed on untracked file: %v", err)
	}

	// 验证文件已被安全物理清理
	if _, err := os.Stat(absPath); !os.IsNotExist(err) {
		t.Errorf("expected untracked file to be removed after discarding hunk, but it still exists")
	}
}

func TestComputeFileDiff_EmptyRepoNoHead(t *testing.T) {
	tmpDir := t.TempDir()
	// 初始化全新的 git 仓库 (不创建任何 commit，无 HEAD)
	cmdInit := gitCmd(tmpDir, "init")
	if err := cmdInit.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	testFile := "new_file.txt"
	absPath := filepath.Join(tmpDir, testFile)
	_ = os.WriteFile(absPath, []byte("hello from empty repo\nsecond line"), 0644)

	// 计算 diff，确保不会因 bad revision 'HEAD' 崩溃
	report, err := ComputeFileDiff(tmpDir, testFile)
	if err != nil {
		t.Fatalf("ComputeFileDiff failed on empty repo without HEAD: %v", err)
	}

	if len(report.Lines) != 2 {
		t.Errorf("expected 2 lines in diff report, got %d", len(report.Lines))
	}
	if !strings.Contains(report.Stats, "新文件") {
		t.Errorf("expected stats to indicate new file, got %s", report.Stats)
	}
}

func TestComputeFileDiff_EmptyUntrackedFile(t *testing.T) {
	tmpDir := t.TempDir()
	cmdInit := gitCmd(tmpDir, "init")
	if err := cmdInit.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	testFile := "empty_new.txt"
	absPath := filepath.Join(tmpDir, testFile)
	_ = os.WriteFile(absPath, []byte(""), 0644)

	report, err := ComputeFileDiff(tmpDir, testFile)
	if err != nil {
		t.Fatalf("ComputeFileDiff failed on empty file: %v", err)
	}

	if len(report.Lines) != 0 {
		t.Errorf("expected 0 lines for empty file, got %d", len(report.Lines))
	}
	if len(report.Hunks) != 0 {
		t.Errorf("expected 0 hunks for empty file, got %d", len(report.Hunks))
	}
	if !strings.Contains(report.Stats, "+0 行") {
		t.Errorf("expected stats to indicate +0 lines, got %s", report.Stats)
	}
}

func TestComputeFileDiff_NoHeadAddedAndModified(t *testing.T) {
	tmpDir := t.TempDir()
	cmdInit := gitCmd(tmpDir, "init")
	if err := cmdInit.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	testFile := "am_file.txt"
	absPath := filepath.Join(tmpDir, testFile)
	_ = os.WriteFile(absPath, []byte("line1\n"), 0644)

	cmdAdd := gitCmd(tmpDir, "add", testFile)
	if err := cmdAdd.Run(); err != nil {
		t.Fatalf("git add failed: %v", err)
	}

	// Modify the file after adding it to stage
	_ = os.WriteFile(absPath, []byte("line1\nline2"), 0644)

	report, err := ComputeFileDiff(tmpDir, testFile)
	if err != nil {
		t.Fatalf("ComputeFileDiff failed on AM file: %v", err)
	}

	if len(report.Lines) != 3 {
		t.Errorf("expected 3 lines in diff report for AM file, got %d", len(report.Lines))
	}
	if !strings.Contains(report.Stats, "1 行新增") {
		t.Errorf("expected stats to indicate 1 line added, got %s", report.Stats)
	}
}

func TestComputeFileDiff_DeletedFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tcode_diff_del_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cmdInit := gitCmd(tmpDir, "init")
	if err := cmdInit.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	testFile := "to_delete.txt"
	absPath := filepath.Join(tmpDir, testFile)
	_ = os.WriteFile(absPath, []byte("content"), 0644)
	_ = gitCmd(tmpDir, "add", testFile).Run()
	_ = gitCmd(tmpDir, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "init").Run()

	// 现在从磁盘删除该文件产生 D 状态
	_ = os.Remove(absPath)

	report, err := ComputeFileDiff(tmpDir, testFile)
	if err != nil {
		t.Fatalf("ComputeFileDiff failed on deleted file: %v", err)
	}
	if report.Stats == "" || !strings.Contains(report.Stats, "删除") {
		t.Errorf("expected stats to indicate deletion, got %q", report.Stats)
	}

	// 测试无 HEAD 仓库中暂存后被删除的文件 (diffOut 为空且 os.ReadFile 失败)
	tmpDir2, err := os.MkdirTemp("", "tcode_diff_nohead_del_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir2)

	_ = gitCmd(tmpDir2, "init").Run()
	testFile2 := "nohead_del.txt"
	absPath2 := filepath.Join(tmpDir2, testFile2)
	_ = os.WriteFile(absPath2, []byte("temp"), 0644)
	_ = gitCmd(tmpDir2, "add", testFile2).Run()
	_ = os.Remove(absPath2) // 删除了暂存的新增文件

	report2, err := ComputeFileDiff(tmpDir2, testFile2)
	if err != nil {
		t.Fatalf("ComputeFileDiff failed on no-HEAD deleted file: %v", err)
	}
	if report2.Stats == "" || !strings.Contains(report2.Stats, "删除") {
		t.Errorf("expected stats to indicate deletion for no-HEAD file, got %q", report2.Stats)
	}
	if len(report2.Lines) == 0 {
		t.Errorf("expected non-empty lines for no-HEAD deleted file report")
	}
}



