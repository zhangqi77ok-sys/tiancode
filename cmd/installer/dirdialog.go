//go:build windows

// 安装目录的选择与解析。
//
// 为什么需要这一层：安装确认框是 MessageBox，只有「继续 / 取消」，用户双击安装包时
// 改不了目录（只能装到写死的默认位置）。这里用系统自带的文件夹选择对话框
// （System.Windows.Forms.FolderBrowserDialog，经 PowerShell 驱动）让用户自己选，
// 选完仍走原有确认框——语义上"点继续才算同意"保持不变。
//
// 为什么结果要走 Base64：安装器是 GUI 子系统程序，PowerShell 的标准输出跟随本机
// OEM 代码页（中文系统即 936），中文路径按 UTF-8 解码必然乱码。Base64(UTF-8) 是与
// 控制台编码无关的表示。脚本本身不走 Base64（-EncodedCommand）而直接作为 -Command
// 的参数传入：argv 是 UTF-16 宽字符、不经代码页转换，中文安全。

package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// dirPickCanceled 表示用户在目录对话框里点了「取消」：是主动放弃，不是失败。
var dirPickCanceled = errors.New("用户取消了目录选择")

// cancelMarker 是 PowerShell 侧表示"用户取消"的输出标记。
// 为什么要标记而不是用退出码：脚本自身出错时也会非零退出，只看退出码无法区分
// "用户放弃"和"对话框起不来"，会把环境问题误报成用户取消。
const cancelMarker = "##CANCEL##"

// folderDialogScript 是目录选择脚本（占位符填描述、起始目录、取消标记）。
// ShowNewFolderButton：允许用户在对话框里直接新建目录名（否则只能选已有目录）。
// SelectedPath 指向不存在的目录时行为不确定，故填 nearestExistingDir 的结果。
const folderDialogScript = `Add-Type -AssemblyName System.Windows.Forms
$dlg = New-Object System.Windows.Forms.FolderBrowserDialog
$dlg.Description = %s
$dlg.SelectedPath = %s
$dlg.ShowNewFolderButton = $true
if ($dlg.ShowDialog() -ne [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write(%s) }
else { [Console]::Out.Write([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($dlg.SelectedPath))) }
`

// pickInstallDir 弹目录选择对话框，返回用户选定的安装目录。
// 用户点取消时返回 dirPickCanceled（调用方应静默退出，不当作失败）。
func pickInstallDir(defaultDir string) (string, error) {
	script := fmt.Sprintf(folderDialogScript,
		psQuote("选择 tiancode 的安装目录"),
		psQuote(nearestExistingDir(defaultDir)),
		psQuote(cancelMarker))
	out, err := runPowerShell(script)
	if err != nil {
		return "", err
	}
	dir, err := decodePickedDir(out)
	if err != nil {
		return "", err
	}
	if dir == "" { // 取消标记：调用方据此静默退出
		return "", dirPickCanceled
	}
	return dir, nil
}

// decodePickedDir 解析 PowerShell 返回的目录（Base64(UTF-8)），取消时返回空串。
func decodePickedDir(out string) (string, error) {
	s := strings.TrimSpace(out)
	if s == cancelMarker {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("目录选择对话框返回了无法解析的内容：%w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// nearestExistingDir 返回从 dir 向上第一个已存在的目录。
//
// 为什么需要：FolderBrowserDialog 的 SelectedPath 指向不存在的目录时行为不确定
// （跳到"我的文档"或上次位置），而首次安装时默认安装目录必然还不存在。
func nearestExistingDir(dir string) string {
	for {
		if pathExists(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir { // 已到盘符根，再上没有可回退的目录
			return dir
		}
		dir = parent
	}
}

// runPowerShell 隐藏窗口执行 PowerShell 脚本并取回标准输出。
// 显式 -STA：FolderBrowserDialog 依赖单线程单元；-NonInteractive 只禁止控制台提示，
// 不影响对话框本身（两者必须同时给，否则某些环境仍会弹控制台错误提示）。
// 不用 -EncodedCommand：实测本机 powershell.exe 以该参数启动直接失败（exit=-1、无任何
// 输出），诊断不出原因，不能把交互安装建在它上面。
func runPowerShell(script string) (string, error) {
	cmd := exec.Command(powershellExe(), "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
	// stdout 单独接（混进 stderr 会破坏 Base64 载荷）；stderr 只在失败时用于说明原因。
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("调用目录选择对话框失败：%w（%s）", err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// psQuote 把字符串包成 PowerShell 单引号字面量：内部单引号须翻倍，否则目录名里的
// 单引号（如 C:\Users\O'Brien）会提前闭合字面量，让脚本变成语法错误。
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// resolveInstallDir 返回安装目标目录：未指定时用默认目录。
func resolveInstallDir(param string) string {
	if p := strings.TrimSpace(param); p != "" {
		return p
	}
	return defaultInstallDir()
}

// resolveUninstallDir 返回卸载目标目录：显式参数 > 注册表记录的安装位置 > 默认目录。
//
// 为什么参数优先：从「应用和功能」拉起时注册表就是本安装；从命令行/脚本传 -dir 时
// 那是本次明确的意图，反过来（优先注册表）会让参数形同虚设。
// 为什么读注册表：注册表里记着实际安装位置，是"这次装到哪"的唯一权威记录；只认默认
// 目录会让装到别处的安装卸错地方。两处都没有（老安装从未写 InstallLocation）才回退默认。
func resolveUninstallDir(param, recorded string) string {
	if p := strings.TrimSpace(param); p != "" {
		return p
	}
	if r := strings.TrimSpace(recorded); r != "" {
		return r
	}
	return defaultInstallDir()
}
