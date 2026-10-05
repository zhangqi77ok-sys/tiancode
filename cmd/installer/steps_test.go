//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// writeFile 写一个小文件，测试用。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写 %s 失败：%v", path, err)
	}
}

// readFile 读回文件内容，测试用。
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", path, err)
	}
	return string(data)
}

// 覆盖写失败后，旧版必须原样回来。
//
// 背景：安装是"覆盖同一个 tiancode.exe"。若直接覆盖，中途失败就会把用户能用的
// 旧版毁掉——从"升级失败"变成"什么都没了"。这条锁住备份/还原的完整闭环。
func TestFileGuard_RestoresOverwrittenFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	writeFile(t, target, "old-version")

	g := fileGuard{path: target}
	if err := g.prepare(); err != nil {
		t.Fatalf("prepare 失败：%v", err)
	}
	if got := readFile(t, target); got != "old-version" {
		t.Fatalf("prepare 不应改变原文件内容，实际 %q", got)
	}
	writeFile(t, target, "new-version")

	g.restore()
	if got := readFile(t, target); got != "old-version" {
		t.Errorf("回滚后应恢复旧版内容，实际 %q", got)
	}
	if pathExists(target + backupSuffix) {
		t.Error("回滚后不应残留备份文件")
	}
}

// 首次安装（目标文件不存在）回滚后必须彻底消失，不能留下半个程序。
func TestFileGuard_RemovesFileWhenNoBackup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")

	g := fileGuard{path: target}
	if err := g.prepare(); err != nil {
		t.Fatalf("prepare 失败：%v", err)
	}
	writeFile(t, target, "fresh")

	g.restore()
	if pathExists(target) {
		t.Error("无备份时回滚应删除本次写入的文件")
	}
}

// 全流程成功后备份必须被清掉，不能在安装目录里留下 *.tiancode-bak。
func TestFileGuard_CommitRemovesBackup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	writeFile(t, target, "old-version")

	g := fileGuard{path: target}
	if err := g.prepare(); err != nil {
		t.Fatalf("prepare 失败：%v", err)
	}
	writeFile(t, target, "new-version")
	g.commit()

	if pathExists(target + backupSuffix) {
		t.Error("commit 后不应残留备份文件")
	}
	if got := readFile(t, target); got != "new-version" {
		t.Errorf("commit 不应改动新文件内容，实际 %q", got)
	}
}

// 撤销必须逆序：先撤销"后做的"，否则中间状态会被后续步骤依赖。
// 这里用两个共享文件断言顺序——步骤 2 依赖步骤 1 的产物，顺序反了就会删错文件。
func TestRollback_UndoesInReverseOrder(t *testing.T) {
	var order []string
	var rb rollback
	rb.add(func() { order = append(order, "first") })
	rb.add(func() { order = append(order, "second") })
	rb.undo()

	if len(order) != 2 || order[0] != "second" || order[1] != "first" {
		t.Errorf("撤销顺序应为 [second first]，实际 %v", order)
	}
}

// 我们创建的目录在回滚后应被清掉；但目录里若已有用户自己的文件，绝不能顺手删。
func TestDirGuard(t *testing.T) {
	t.Run("空目录被删除", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "created")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dirGuard{path: dir}.rollback()()
		if pathExists(dir) {
			t.Error("回滚应删除我们创建的空目录")
		}
	})

	t.Run("非空目录保留", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "created")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "user-data.txt"), "mine")
		dirGuard{path: dir}.rollback()()
		if !pathExists(filepath.Join(dir, "user-data.txt")) {
			t.Error("回滚不得删除用户自己放在安装目录里的文件")
		}
	})
}

// 注册项原本不存在时，回滚必须删键——否则"应用和功能"里会留一个指向空目录的幽灵项。
func TestRegistryGuard_RemovesKeyWhenItDidNotExist(t *testing.T) {
	key := testKeyPath(t)
	g := newRegistryGuard(key)
	g.capture()

	writeStringValue(t, key, "DisplayName", "tiancode")
	g.restore()

	if _, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE); err == nil {
		t.Error("回滚应删除原本不存在的注册项键")
	}
}

// 注册项原本存在时，回滚必须把旧值原样写回，不能留着新装的路径。
func TestRegistryGuard_RestoresPreviousValues(t *testing.T) {
	key := testKeyPath(t)
	writeStringValue(t, key, "InstallLocation", `C:\old-install`)
	writeStringValue(t, key, "DisplayVersion", "0.0.1")

	g := newRegistryGuard(key)
	g.capture()

	writeStringValue(t, key, "InstallLocation", `C:\new-install`)
	writeStringValue(t, key, "DisplayVersion", "0.0.99")
	g.restore()

	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("回滚后键应存在：%v", err)
	}
	defer k.Close()
	for name, want := range map[string]string{
		"InstallLocation": `C:\old-install`,
		"DisplayVersion":  "0.0.1",
	} {
		got, _, err := k.GetStringValue(name)
		if err != nil {
			t.Errorf("回滚后 %s 应可读：%v", name, err)
			continue
		}
		if got != want {
			t.Errorf("回滚后 %s = %q，want %q", name, got, want)
		}
	}
}

// 步骤失败后，安装目录必须回到"等于没建过"的状态。
//
// 为什么用"载荷为空"触发：这是唯一能在普通构建（无 installer_payload 标签）
// 下确定性触发的失败点，不需要真的构造"写到一半炸掉"。
// 载荷非空时 installFiles 首步就会失败，不会写任何文件——正是回滚要保证的。
func TestInstallFiles_RollbackLeavesNothing(t *testing.T) {
	if len(appBinary) > 0 {
		t.Skip("该构建内嵌了真实载荷，失败路径无法确定性构造（仅 release 构建）")
	}
	dir := filepath.Join(t.TempDir(), "target")
	var rb rollback
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rb.add(dirGuard{path: dir}.rollback())

	if _, err := installFiles(dir, &rb); err == nil {
		t.Fatal("载荷为空时 installFiles 必须报错（否则会写出 0 字节的 exe）")
	}
	rb.undo()

	if pathExists(dir) {
		t.Error("回滚后安装目录不应残留")
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		t.Errorf("回滚后目录应为空，实际残留 %d 项", len(entries))
	}
}

// testKeyPath 返回一个本次测试专用的注册表键路径，并在测试结束时清理。
//
// 刻意不用真实的 Uninstall\tiancode：那个键属于用户已安装的版本，
// 单测覆盖它会牵连真实安装（install-smoke.ps1 的头注释记录过这个教训）。
func testKeyPath(t *testing.T) string {
	t.Helper()
	key := `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\tiancode-rollback-test`
	t.Cleanup(func() {
		if _, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE); err == nil {
			registry.DeleteKey(registry.CURRENT_USER, key)
		}
	})
	// 清掉上一次可能残留的值，保证 capture 看到干净状态。
	if k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.SET_VALUE|registry.WRITE); err == nil {
		k.Close()
		registry.DeleteKey(registry.CURRENT_USER, key)
	}
	return key
}

// writeStringValue 写一个字符串值，测试用。
func writeStringValue(t *testing.T, key, name, value string) {
	t.Helper()
	k, _, err := registry.CreateKey(registry.CURRENT_USER, key, registry.WRITE)
	if err != nil {
		t.Fatalf("创建测试键失败：%v", err)
	}
	defer k.Close()
	if err := k.SetStringValue(name, value); err != nil {
		t.Fatalf("写 %s=%s 失败：%v", name, value, err)
	}
}
