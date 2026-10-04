package gittool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 0.0.30 用户审查 R1/R3 的回归锁：
// ① DiffHEAD 必须含**已暂存**块（提交说明的原料边界——裸 git diff 漏掉这一半）；
// ② git 读路径（buildArgs / DiffFile）必须拒绝 ../ 与绝对路径：会话根常是仓库
//    子目录，而 git 的 pathspec 认 "../"，会读到同一仓库里工作区之外的兄弟目录；
// ③ DiffFile 必须有界（此前注释声称"与 Diff 同规"，实现却直接返回未截断输出）；
// ④ StageAllAndCommit 两步失败语义分开：add 失败=索引未动，commit 失败=索引已暂存。

// TestDiffHEAD_IncludesStagedBlocks 锁住提交说明的原料边界。
func TestDiffHEAD_IncludesStagedBlocks(t *testing.T) {
	tool := newRepo(t)
	root := tool.root
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-qm", "init")

	// 改已跟踪文件并暂存
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n// STAGED\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "a.go")

	head, err := DiffHEAD(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(head, "STAGED") {
		t.Fatalf("DiffHEAD 未含已暂存块：%q", head)
	}
	// 测试前提：此刻裸 diff 里没有它（旧实现用它 → 说明漏掉已暂存的一半变更）
	plain, err := Diff(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "STAGED") {
		t.Fatalf("前提不成立：裸 diff 竟含已暂存块 %q", plain)
	}
}

// TestGitReadPaths_RejectOutsideWorkspace 锁住工作区围栏：../ 与绝对路径不进 git。
func TestGitReadPaths_RejectOutsideWorkspace(t *testing.T) {
	tool := newRepo(t)
	sub := filepath.Join(tool.root, "sub") // 会话根 = 仓库子目录
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	bad := []string{
		"../sibling/secret.txt",
		"..",
		`..\sibling\x`,
		"/etc/passwd",
		`C:\Windows\win.ini`,
	}
	for _, p := range bad {
		if _, err := buildArgs("diff", p, 0); err == nil {
			t.Fatalf("buildArgs 接受越界路径 %q", p)
		}
		if _, err := buildArgs("status", p, 0); err == nil {
			t.Fatalf("buildArgs(status) 接受越界路径 %q", p)
		}
		if _, err := buildArgs("log", p, 10); err == nil {
			t.Fatalf("buildArgs(log) 接受越界路径 %q", p)
		}
		if _, err := DiffFile(sub, p); err == nil {
			t.Fatalf("DiffFile 接受越界路径 %q", p)
		}
	}
	// 根内相对路径照常放行（别顺手把功能关掉）
	if _, err := buildArgs("diff", "sub/a.go", 0); err != nil {
		t.Fatalf("根内路径被误拒：%v", err)
	}
	if _, err := DiffFile(sub, "a.go"); err != nil {
		t.Fatalf("根内 DiffFile 被误拒：%v", err)
	}
}

// TestDiffFile_Bounded 锁住"注释说有截断，实现就得有"。
func TestDiffFile_Bounded(t *testing.T) {
	tool := newRepo(t)
	root := tool.root
	big := strings.Repeat("这是一行用于撑大 diff 的中文内容\n", 20000)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-qm", "init")
	// 整体重写（不是追加一行）：diff 会是整个文件，远超 outputLimit。
	// 追加一行只产生 1 行 diff，压根不触发截断路径——那种测法是假绿。
	big2 := strings.Repeat("这是改写后的另一行中文内容用于撑大 diff\n", 20000)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(big2), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := DiffFile(root, "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > outputLimit+512 {
		t.Fatalf("DiffFile 未截断：%d 字节（上限 %d）", len(out), outputLimit)
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("截断未标注丢了多少：%q", out[max(0, len(out)-160):])
	}
}

// TestStageAllAndCommit_CommitFailureSaysIndexStaged 锁住失败语义：
// add 已成功 = 索引已被全部暂存，错误必须说出来（否则用户以为什么都没发生）。
func TestStageAllAndCommit_CommitFailureSaysIndexStaged(t *testing.T) {
	tool := newRepo(t)
	root := tool.root
	// 制造"add 成功但 commit 无内容可提交"：先改文件再改回，索引最终无差异
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n// 临时\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-qm", "init")
	// 改一个未跟踪文件 → add -A 会把它暂存 → commit 成功；所以这里改为：
	// 让 commit 因钩子/身份缺失失败不现实，改用「空提交」路径——
	// add -A 什么都不加，commit 报 nothing to commit。
	out, err := StageAllAndCommit(root, "探针")
	if err == nil {
		t.Skip("本环境允许空提交（无可断言的失败路径）")
	}
	if !strings.Contains(err.Error(), "暂存") {
		t.Fatalf("commit 失败未说明索引已暂存：%v", err)
	}
	if out != "" {
		t.Fatalf("失败时不应有输出：%q", out)
	}
}

// 0.0.30 审查 R2：Git 面板点开的单文件 diff 必须是「相对 HEAD 的全部改动」，
// 与提交说明（DiffHEAD）同源。否则「已暂存、工作区已干净」的文件点开是空的，
// 人在面板里看不到模型写进提交说明的那些行。
func TestDiffFileHEAD_IncludesStagedBlocks(t *testing.T) {
	tool := newRepo(t)
	root := tool.root
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-qm", "init")

	// 只暂存、不留工作区改动：git diff（未暂存口径）在这里必然是空的
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n// ONLY_STAGED\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "a.go")

	head, err := DiffFileHEAD(root, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(head, "ONLY_STAGED") {
		t.Fatalf("DiffFileHEAD 未含已暂存块：%q", head)
	}
	// 测试前提：同一时刻未暂存口径为空（旧实现用它 → 面板与提交说明口径不一致）
	plain, err := DiffFile(root, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if plain != "" {
		t.Fatalf("前提不成立：未暂存 diff 竟非空 %q", plain)
	}
}

// DiffFileHEAD 的围栏与有界必须与 DiffFile 同规（别因为多一个方法就少一道闸）。
func TestDiffFileHEAD_FenceAndBound(t *testing.T) {
	tool := newRepo(t)
	root := tool.root
	sub := filepath.Join(root, "sub") // 会话根 = 仓库子目录
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	// 越界路径在 checkRelPath 就被拒，走不到 git
	for _, p := range []string{"../sibling/secret.txt", "..", `/etc/passwd`, `C:\Windows\win.ini`} {
		if _, err := DiffFileHEAD(sub, p); err == nil {
			t.Fatalf("DiffFileHEAD 接受越界路径 %q", p)
		}
	}

	// 还没有任何提交时 `git diff HEAD` 本身就不成立（fatal: bad revision 'HEAD'）。
	// 必须显式报错——静默返回空会让"仓库还没有提交"看起来像"这个文件没有改动"。
	if _, err := DiffFileHEAD(root, "a.go"); err == nil {
		t.Fatal("空仓库（无 HEAD）必须显式报错，不得静默返回空 diff")
	}

	// 有首个提交后：根内相对路径照常放行
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-qm", "init")
	if _, err := DiffFileHEAD(sub, "a.go"); err != nil {
		t.Fatalf("根内 DiffFileHEAD 被误拒：%v", err)
	}

	// 大文件必须有界：整体重写让 diff 远超 outputLimit
	big := strings.Repeat("这是一行用于撑大 diff 的中文内容\n", 20000)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-qm", "init")
	big2 := strings.Repeat("这是改写后的另一行中文内容用于撑大 diff\n", 20000)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(big2), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := DiffFileHEAD(root, "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > outputLimit+512 {
		t.Fatalf("DiffFileHEAD 未截断：%d 字节（上限 %d）", len(out), outputLimit)
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("截断未标注丢了多少：%q", out[max(0, len(out)-160):])
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
