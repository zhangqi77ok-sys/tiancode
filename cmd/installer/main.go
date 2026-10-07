//go:build windows

// Package main 是 tiancode 的原生 Windows 安装器（无外部依赖、离线可用）。
//
// 做什么：把内嵌的应用 exe 安装到用户目录，创建桌面与开始菜单快捷方式与
// "应用和功能"卸载注册项；-uninstall 反向清理。零外部工具（不需要 NSIS/Inno）。
// 参照 legacy cmd/installer 的既有做法（MessageBoxW + registry），但收敛为：
// 单 payload 内嵌 + 静默模式（-quiet，供自动化验证与脚本化部署）。
//
// 构建：由 scripts/release.ps1 驱动（先产出应用 exe 到 payload/，再构建本器）。
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

// version 由 release.ps1 通过 -ldflags "-X main.version=<VERSION>" 注入。
var version = "dev"

// MessageBoxW 标志位（仅用到这三个，按需扩展）。
const (
	mbOK        = 0x00000000
	mbOKCancel  = 0x00000001
	mbIconError = 0x00000010
	mbIconInfo  = 0x00000040
	mbIconQuest = 0x00000020
	idOK        = 1
	idCancel    = 2
)

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

// messageBox 弹出系统原生消息框（静默模式下不调用）。
func messageBox(title, text string, style uint) int {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	r, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), uintptr(style))
	return int(r)
}

func main() {
	// -dir 默认为空串而非 defaultInstallDir()：卸载需要区分"没给参数"（该走注册表里
	// 实际安装位置）与"给了一个目录"，只能靠空值判断。安装侧由 resolveInstallDir 兜默认值。
	dirFlag := flag.String("dir", "", "安装目录（默认 %LOCALAPPDATA%\\Programs\\tiancode）")
	uninstall := flag.Bool("uninstall", false, "卸载已安装的 tiancode")
	quiet := flag.Bool("quiet", false, "静默模式：不弹对话框（供自动化/脚本部署）")
	noDesktop := flag.Bool("no-desktop-shortcut", false, "不创建桌面快捷方式（仅创建开始菜单快捷方式）")
	// relaunch（0.0.20 自更新）：装完重启应用——自更新流程是"下载 → 拉起安装器
	// (-quiet -relaunch) → 应用自退"，没有这一步用户得手动再点开。
	relaunch := flag.Bool("relaunch", false, "安装完成后重启应用（自更新流程用）")
	flag.Parse()

	if *uninstall {
		// 卸载目录：参数优先，没有就读注册表里记录的安装位置（见 resolveUninstallDir）——
		// 旧版 UninstallString 不带目录，只认默认目录会卸错地方。
		dir := resolveUninstallDir(*dirFlag, readRecordedInstallDir())
		// 卸载同样先握手（0.0.38）：请运行中的实例优雅退出（空闲退 / 忙拒退），
		// 运行中的 exe 被锁定，删除必然失败。不替用户杀进程（TestNoForcedAppKill）。
		if err := ensureAppClosed(*quiet, dir); err != nil {
			fail("tiancode 正在运行", err, *quiet)
			return
		}
		if err := doUninstall(dir); err != nil {
			fail("卸载失败", err, *quiet)
			return
		}
		if !*quiet {
			messageBox("tiancode 卸载", "已卸载完成。", mbOK|mbIconInfo)
		}
		return
	}

	installDir := resolveInstallDir(*dirFlag)
	if !*quiet {
		// 交互模式先让用户改目录：MessageBox 只有「继续 / 取消」，路径改不了。
		// 用户点取消 = 主动放弃安装，静默退出（不算失败，不弹错误框）。
		dir, err := pickInstallDir(installDir)
		if errors.Is(err, dirPickCanceled) {
			return
		}
		if err != nil {
			fail("选择安装目录失败", err, false)
			return
		}
		installDir = dir
		msg := fmt.Sprintf("将安装 tiancode %s 到：\n%s\n\n继续？", version, installDir)
		if messageBox("tiancode 安装", msg, mbOKCancel|mbIconQuest) != idOK {
			return
		}
	}
	if err := ensureAppClosed(*quiet, installDir); err != nil {
		fail("tiancode 正在运行", err, *quiet)
		return
	}
	if err := doInstall(installDir, !*noDesktop); err != nil {
		fail("安装失败", err, *quiet)
		return
	}
	if !*quiet {
		msg := "已创建桌面与开始菜单快捷方式，可从“应用和功能”卸载。"
		if *noDesktop {
			msg = "已创建开始菜单快捷方式（按参数跳过桌面快捷方式），可从“应用和功能”卸载。"
		}
		messageBox("tiancode 安装完成", msg, mbOK|mbIconInfo)
	}
	if *relaunch {
		// 重启用独立进程路径启动新版；失败只记日志不报错框——安装本身已成功，
		// "没自动重启"不该伪装成安装失败（用户从开始菜单点开即可）。
		exe := filepath.Join(installDir, "tiancode.exe")
		cmd := exec.Command(exe)
		cmd.Dir = installDir
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "relaunch failed: %v\n", err)
		}
	}
}

// fail 报告失败：写明结果并**统一退码 1**（0.0.38 修复 0.0.31 登记的缺陷——
// 此前交互模式只弹框不 exit，任何失败（含"请先退出"中止）都返回 exit 0，
// 自动化判定会把"没装成"读成"成功"）。
func fail(title string, err error, quiet bool) {
	if quiet {
		fmt.Fprintf(os.Stderr, "%s: %v\n", title, err)
	} else {
		messageBox(title, err.Error(), mbOK|mbIconError)
	}
	os.Exit(1)
}
