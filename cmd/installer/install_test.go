//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 回归锁：安装创建的快捷方式集合，必须与卸载删除的集合逐项一致。
//
// 为什么需要这条测试：卸载若不删除某个快捷方式，用户开始菜单/桌面上会留下
// 指向已删程序的孤儿 .lnk（点开即报错），而这类残留极难被手工发现。
func TestShortcuts_InstallUninstallSymmetry(t *testing.T) {
	created := installShortcuts(true)
	removed := uninstallShortcuts()

	if len(created) == 0 {
		t.Fatal("安装未创建任何快捷方式")
	}
	if len(created) != len(removed) {
		t.Fatalf("安装创建 %d 个快捷方式，卸载只删 %d 个：卸载会留下孤儿 .lnk", len(created), len(removed))
	}
	for i := range created {
		if created[i].lnk != removed[i] {
			t.Errorf("第 %d 项不对称：安装创建 %q，卸载删除 %q", i, created[i].lnk, removed[i])
		}
	}
}

// 卸载必须无条件清理两个位置：即使安装时用 -no-desktop-shortcut 跳过了桌面项，
// 历史安装（或用户手工放回）留下的桌面 .lnk 也应被清掉。
func TestShortcuts_UninstallAlwaysCoversBoth(t *testing.T) {
	withDesktop := installShortcuts(true)
	withoutDesktop := installShortcuts(false)

	if len(withoutDesktop) != len(withDesktop)-1 {
		t.Fatalf("跳过桌面项时应少创建 1 个快捷方式：有桌面 %d 个，无桌面 %d 个",
			len(withDesktop), len(withoutDesktop))
	}
	for _, s := range withoutDesktop {
		if !strings.Contains(s.lnk, "Start Menu") {
			t.Errorf("跳过桌面项后仍创建了非开始菜单快捷方式：%q", s.lnk)
		}
	}
	if got := len(uninstallShortcuts()); got != len(withDesktop) {
		t.Errorf("卸载应始终清理 %d 个位置，实际 %d 个", len(withDesktop), got)
	}
}

// 回归锁：开始菜单与桌面两处快捷方式都必须创建。
//
// 背景：legacy 安装器创建「桌面 + 开始菜单」两个快捷方式
// （legacy cmd/installer/main.go:217-222），重建版只保留了开始菜单，
// 用户在桌面找不到入口。本测试防止该能力再次丢失。
func TestShortcuts_BothStartMenuAndDesktop(t *testing.T) {
	created := installShortcuts(true)

	var startMenu, desktop int
	for _, s := range created {
		if filepath.Base(s.lnk) != appName+".lnk" {
			t.Errorf("快捷方式命名应为 %s.lnk，实际 %q", appName, filepath.Base(s.lnk))
		}
		if s.workDir == "" {
			t.Errorf("%s 的工作目录为空：快捷方式启动后落点不确定", s.lnk)
		}
		if strings.Contains(s.lnk, "Start Menu") {
			startMenu++
		} else {
			desktop++
		}
	}

	if startMenu != 1 {
		t.Errorf("开始菜单快捷方式应恰好 1 个，实际 %d 个", startMenu)
	}
	if desktop != 1 {
		t.Errorf("桌面快捷方式应恰好 1 个，实际 %d 个（这正是 legacy 有而重建版丢失的能力）", desktop)
	}
}

// 桌面目录可能被 OneDrive 等重定向，注册表不可用时必须回退到 %USERPROFILE%\Desktop，
// 而不是让安装失败或写到不存在的路径。
func TestDesktopDirFallback_UsesUserProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)

	if got, want := desktopDirFallback(), filepath.Join(home, "Desktop"); got != want {
		t.Errorf("desktopDirFallback() = %q, want %q", got, want)
	}
}

// 策略锁：恰好一个快捷方式是必需的（开始菜单）。
// 若把开始菜单也降级为可选，用户可能在"安装成功"但没有任何启动入口的情况下收场。
func TestShortcuts_OnlyStartMenuIsRequired(t *testing.T) {
	required := 0
	for _, s := range installShortcuts(true) {
		if !s.required {
			continue
		}
		required++
		if !strings.Contains(s.lnk, "Start Menu") {
			t.Errorf("必需项应为开始菜单快捷方式，实际 %q", s.lnk)
		}
	}
	if required != 1 {
		t.Errorf("应有且仅有 1 个必需快捷方式，实际 %d 个", required)
	}
}

// 卸载不得误删「另一处安装」的注册项。
//
// 背景（实测踩过）：注册表键名固定为 Uninstall\tiancode，是"应用和功能"里的一项。
// 旧实现按名字无条件删除，于是用隔离目录做卸载验证时，把用户**正式安装**的那一份
// 从"应用和功能"里一起抹掉了。现在必须先核对 InstallLocation 归属。
func TestUninstallOwnsEntry(t *testing.T) {
	own := `C:\Users\tester\AppData\Local\Programs\tiancode`
	cases := []struct {
		name     string
		recorded string
		dir      string
		want     bool
	}{
		{"同一目录", own, own, true},
		{"Windows 路径大小写不敏感", `C:\Users\tester\AppData\Local\Programs\TIANCODE`, own, true},
		{"尾部分隔符差异", own + `\`, own, true},
		{"另一处隔离安装（smoke）", own, own + "-smoke", false},
		{"完全不同的目录", `C:\Other\tiancode`, own, false},
		{"注册项缺 InstallLocation", "", own, true}, // 无从判断时保守允许，避免用户卸不掉
	}
	for _, c := range cases {
		if got := uninstallOwnsEntry(c.recorded, c.dir); got != c.want {
			t.Errorf("%s: uninstallOwnsEntry(%q, %q) = %v, want %v", c.name, c.recorded, c.dir, got, c.want)
		}
	}
}

// powershell.exe 不在 System32 根目录（在 System32\WindowsPowerShell\v1.0\），
// 只写裸名会让安装器在精简 PATH 环境下失败（实测：executable file not found）。
// 这里锁「能解析到真实文件」。
func TestPowershellExe_ResolvesToExistingFile(t *testing.T) {
	got := powershellExe()
	if got == "powershell.exe" {
		t.Log("回退到 PATH 解析：本机 %SystemRoot% 下未找到 powershell.exe（异常布局）")
		return
	}
	if filepath.Base(got) != "powershell.exe" {
		t.Errorf("powershellExe() = %q，应以 powershell.exe 结尾", got)
	}
	if st, err := os.Stat(got); err != nil || st.IsDir() {
		t.Errorf("powershellExe() = %q，但该路径不可用：err=%v", got, err)
	}
}

// SystemRoot 指向不含该文件的目录时，必须回退为裸名，
// 而不是返回一个必然不存在的绝对路径（那会让安装 100% 失败且原因难查）。
func TestPowershellExe_FallsBackWhenMissing(t *testing.T) {
	t.Setenv("SystemRoot", t.TempDir())
	if got := powershellExe(); got != "powershell.exe" {
		t.Errorf("powershellExe() = %q, want \"powershell.exe\"", got)
	}
}

// 注册表 Shell Folders 的值是 Windows 风格 %NAME%，而 os.ExpandEnv 只认 $NAME，
// 因此需要自己的展开逻辑，否则会把字面 "%USERPROFILE%\Desktop" 当路径用。
func TestExpandEnvVars(t *testing.T) {
	t.Setenv("TC_TEST_HOME", `C:\Users\tester`)

	cases := []struct {
		in, want string
	}{
		{`%TC_TEST_HOME%\Desktop`, `C:\Users\tester\Desktop`},
		{`%TC_TEST_HOME%`, `C:\Users\tester`},
		{`no-vars-here`, `no-vars-here`},
		// 同一串里多个引用都要展开
		{`%TC_TEST_HOME%/a/%TC_TEST_HOME%`, `C:\Users\tester/a/C:\Users\tester`},
		// 不成对的 % 按字面保留，不能吞掉后续内容
		{`a%UNPAIRED`, `a%UNPAIRED`},
		{`%`, `%`},
		// "%%" 视为字面 %
		{`%%`, `%`},
	}
	for _, c := range cases {
		if got := expandEnvVars(c.in); got != c.want {
			t.Errorf("expandEnvVars(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// 未定义变量展开为空（与 os.Getenv 语义一致），不应残留 % 记号。
	t.Setenv("TC_DEFINED", "x")
	if got := expandEnvVars("%TC_UNDEFINED_XYZ%"); got != "" {
		t.Errorf("未定义变量应展开为空，实际 %q", got)
	}
}
