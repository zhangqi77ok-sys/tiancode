package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcore "tiancode/internal/app"
)

// 右栏「目录」tab 契约：ListWorkspaceDir——
//   - 列会话工作区**一层**：目录在前、名称次序，条目带 isDir/modTime；
//   - 路径校验强度对齐 fstool（绝对路径 / ../ 穿越 / 符号链接逃逸一律拒绝）；
//   - 不存在的目录显式报错；无工作区显式报错——绝不静默返回空列表装作空目录。
func TestBind_ListWorkspaceDir(t *testing.T) {
	root := t.TempDir()
	mkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mkdir("z-dir")
	mkdir("a-dir/sub")
	write("a.txt", "1")
	write("b.txt", "2")

	b, _ := newBindForTest(t, root)

	// 根列表：目录在前、名称次序（同一类内字典序）
	entries, err := b.ListWorkspaceDir("s-tree", "")
	if err != nil {
		t.Fatalf("列根目录失败：%v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if got, want := strings.Join(names, ","), "a-dir,z-dir,a.txt,b.txt"; got != want {
		t.Fatalf("排序不符（目录在前、名称次序）：got %s want %s", got, want)
	}
	if !entries[0].IsDir || entries[2].IsDir {
		t.Fatalf("isDir 标注不符：%+v", entries)
	}
	for _, e := range entries {
		if e.ModTime <= 0 {
			t.Fatalf("modTime 必须带回真实时间戳：%+v", e)
		}
	}

	// 子目录：相对路径逐层下钻（懒加载树的数据源）
	sub, err := b.ListWorkspaceDir("s-tree", "a-dir")
	if err != nil {
		t.Fatalf("列子目录失败：%v", err)
	}
	if len(sub) != 1 || sub[0].Name != "sub" || !sub[0].IsDir {
		t.Fatalf("子目录列表不符：%+v", sub)
	}
	deep, err := b.ListWorkspaceDir("s-tree", "a-dir/sub")
	if err != nil || len(deep) != 0 {
		t.Fatalf("空目录应为空列表且不报错：err=%v entries=%v", err, deep)
	}

	// 穿越 / 绝对路径一律拒绝
	outside := t.TempDir()
	for _, bad := range []string{"../outside", "a-dir/../../outside", outside} {
		if _, err := b.ListWorkspaceDir("s-tree", bad); err == nil {
			t.Fatalf("路径 %q 必须被拒绝", bad)
		}
	}

	// 不存在的目录：显式报错（区别于"存在但为空"）
	if _, err := b.ListWorkspaceDir("s-tree", "nope"); err == nil {
		t.Fatal("不存在的目录必须报错")
	}

	// 工作区内的符号链接指向区外必须拒绝（词法前缀拦不住的那一半）。
	// Windows 创建符号链接需要特权：失败时跳过（非 Windows/有特权环境仍覆盖）。
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "leak")); err != nil {
		t.Skip("无法创建符号链接（需要特权）：", err)
	}
	if _, err := b.ListWorkspaceDir("s-tree", "leak"); err == nil {
		t.Fatal("指向区外的符号链接必须拒绝")
	}
}

// 纯对话（账本无归属且顶栏无工作区）：显式报错，不静默返回空列表。
func TestBind_ListWorkspaceDir_NoWorkspace(t *testing.T) {
	s, err := appcore.NewChatService(appcore.Config{
		DataDir:      t.TempDir(),
		WorkDir:      "", // 纯对话
		ChannelsPath: filepath.Join(t.TempDir(), "channels.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	b := &Bind{chat: s}
	if _, err := b.ListWorkspaceDir("s-nobody", ""); err == nil {
		t.Fatal("无工作区必须报错")
	}
}
