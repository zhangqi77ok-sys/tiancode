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
	"flag"
	"fmt"
	"os"
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
	dirFlag := flag.String("dir", defaultInstallDir(), "安装目录")
	uninstall := flag.Bool("uninstall", false, "卸载已安装的 tiancode")
	quiet := flag.Bool("quiet", false, "静默模式：不弹对话框（供自动化/脚本部署）")
	noDesktop := flag.Bool("no-desktop-shortcut", false, "不创建桌面快捷方式（仅创建开始菜单快捷方式）")
	flag.Parse()

	if *uninstall {
		if err := doUninstall(*dirFlag); err != nil {
			fail("卸载失败", err, *quiet)
			return
		}
		if !*quiet {
			messageBox("tiancode 卸载", "已卸载完成。", mbOK|mbIconInfo)
		}
		return
	}

	if !*quiet {
		msg := fmt.Sprintf("将安装 tiancode %s 到：\n%s\n\n继续？", version, *dirFlag)
		if messageBox("tiancode 安装", msg, mbOKCancel|mbIconQuest) != idOK {
			return
		}
	}
	if err := doInstall(*dirFlag, !*noDesktop); err != nil {
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
}

// fail 报告失败：静默模式写 stderr 并退码 1；交互模式弹错误框。
func fail(title string, err error, quiet bool) {
	if quiet {
		fmt.Fprintf(os.Stderr, "%s: %v\n", title, err)
		os.Exit(1)
	}
	messageBox(title, err.Error(), mbOK|mbIconError)
}
