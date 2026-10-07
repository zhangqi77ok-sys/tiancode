//go:build windows

package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 目录对话框输出解析：取消标记、正常路径、非法载荷。
//
// 为什么锁这条：安装器是 GUI 子系统程序，PowerShell 的输出跟着本机 OEM 代码页走，
// 唯一与编码无关的表示就是 Base64(UTF-8)。解析一旦放宽/收紧，用户会看到
// "乱码路径"或"目录选择失败"，且只在特定代码页的机器上复现。
func TestDecodePickedDir(t *testing.T) {
	t.Run("用户取消", func(t *testing.T) {
		got, err := decodePickedDir(cancelMarker + "\r\n")
		if err != nil {
			t.Fatalf("取消不应报错：%v", err)
		}
		if got != "" {
			t.Errorf("取消应解析为空目录，实际 %q", got)
		}
	})

	t.Run("中文路径", func(t *testing.T) {
		want := `D:\工具\我的 tiancode`
		out := base64.StdEncoding.EncodeToString([]byte(want))
		got, err := decodePickedDir(out)
		if err != nil {
			t.Fatalf("解析失败：%v", err)
		}
		if got != want {
			t.Errorf("decodePickedDir() = %q, want %q", got, want)
		}
	})

	t.Run("非法载荷必须报错而不是当成空目录", func(t *testing.T) {
		// 若这里返回空目录，调用方会把"对话框坏了"当成"用户取消"静默退出——
		// 用户双击安装包却什么都没发生，且没有任何报错。
		if _, err := decodePickedDir("not-base64!!"); err == nil {
			t.Fatal("非法输出必须报错（不能静默当成用户取消）")
		}
	})
}

// 起始目录必须已存在：FolderBrowserDialog 的 SelectedPath 指向不存在的目录时
// 行为不确定（跳到"我的文档"或上次位置），而首次安装时默认目录必然还不存在。
func TestNearestExistingDir(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "Programs", "tiancode", "nested")
	if err := os.MkdirAll(filepath.Join(root, "Programs"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := nearestExistingDir(deep)
	want := filepath.Join(root, "Programs")
	if !samePath(got, want) {
		t.Errorf("nearestExistingDir(%q) = %q, want %q（应回退到最近存在的祖先）", deep, got, want)
	}

	if !samePath(nearestExistingDir(root), root) {
		t.Errorf("目录本身存在时应原样返回，实际 %q", nearestExistingDir(root))
	}
}

// PowerShell 单引号字面量必须转义内部单引号。
//
// 为什么锁这条：目录名里出现单引号（如 C:\Users\O'Brien）时若不翻倍，字面量会提前
// 闭合，脚本变成语法错误——用户只是恰好选了带引号的目录，却看到"选择安装目录失败"。
func TestPsQuote_EscapesSingleQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{`D:\tools\tiancode`, `'D:\tools\tiancode'`},
		{`C:\Users\O'Brien`, `'C:\Users\O''Brien'`},
		{"中文目录", "'中文目录'"},
		{"", "''"},
	}
	for _, c := range cases {
		if got := psQuote(c.in); got != c.want {
			t.Errorf("psQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 安装目录：给了就用，没给（空串/纯空格）才用默认目录。
func TestResolveInstallDir(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\tester\AppData\Local`)

	if got, want := resolveInstallDir(`D:\tools\tiancode`), `D:\tools\tiancode`; got != want {
		t.Errorf("显式目录应原样使用，实际 %q", got)
	}
	if got, want := resolveInstallDir(""), defaultInstallDir(); got != want {
		t.Errorf("未指定时应回退默认目录，实际 %q，want %q", got, want)
	}
	if got, want := resolveInstallDir("   "), defaultInstallDir(); got != want {
		t.Errorf("纯空白的 -dir 应视作未指定，实际 %q，want %q", got, want)
	}
}

// 卸载目录三级优先级：参数 > 注册表实际安装位置 > 默认目录。
//
// 为什么必须读注册表：装到自定义目录的安装，卸载入口可能从任意位置被拉起；
// 只认默认目录会把用户的程序留在盘上。
func TestResolveUninstallDir(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\tester\AppData\Local`)
	own := `C:\Users\tester\AppData\Local\Programs\tiancode`
	other := `D:\tools\tiancode`

	cases := []struct {
		name     string
		param    string
		recorded string
		want     string
	}{
		{"参数优先于注册表（装到别处后按参数卸）", other, own, other},
		{"无参数时用注册表记录的实际目录", "", own, own},
		{"参数与注册表都无时回退默认目录", "", "", defaultInstallDir()},
		{"注册表记录是空白等同没有", "", "  ", defaultInstallDir()},
		{"参数为空白时仍用注册表", "  ", own, own},
	}
	for _, c := range cases {
		if got := resolveUninstallDir(c.param, c.recorded); !samePath(got, c.want) {
			t.Errorf("%s: resolveUninstallDir(%q, %q) = %q, want %q", c.name, c.param, c.recorded, got, c.want)
		}
	}
}

// 「应用和功能」里的卸载命令行必须写明实际安装目录。
//
// 背景：旧实现只写 `setup.exe -uninstall`，卸载端只能退到默认目录——用户把程序装到
// 自定义目录后，卸载会报"文件不存在"或留下残骸。
func TestUninstallCommand_CarriesInstallDir(t *testing.T) {
	setup := `D:\tools\tiancode\tiancode-setup.exe`
	dir := `D:\tools\tiancode`

	got := uninstallCommand(setup, dir)
	for _, want := range []string{setup, "-uninstall", `-dir "` + dir + `"`} {
		if !strings.Contains(got, want) {
			t.Errorf("卸载命令行 %q 缺少 %q", got, want)
		}
	}
	if !strings.HasPrefix(got, `"`+setup+`"`) {
		t.Errorf("卸载命令行应以带引号的 setup 路径开头（路径可能含空格），实际 %q", got)
	}
}
