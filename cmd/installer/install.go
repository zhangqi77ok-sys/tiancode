//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// appBinary 由构建标签决定来源（见 payload_embed.go / payload_stub.go）：
// 常规构建为空占位（保证 go build ./... 在任何环境可过），
// release.ps1 以 -tags installer_payload 构建时才内嵌真实载荷。

const (
	appName       = "tiancode"
	appExeName    = "tiancode.exe"
	setupCopyName = "tiancode-setup.exe"
	uninstallKey  = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\tiancode`
	// createNoWindow 防 PowerShell 子进程弹黑框（legacy 反复踩过的坑）。
	createNoWindow = 0x08000000
)

// defaultInstallDir 返回默认安装目录：%LOCALAPPDATA%\Programs\tiancode。
// 为什么装到 LOCALAPPDATA 而非 Program Files：用户级安装无需管理员权限。
func defaultInstallDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "Programs", appName)
}

// doInstall 安装：写应用 exe → 复制自身（卸载入口）→ 快捷方式 → 注册表项。
func doInstall(dir string) error {
	if len(appBinary) == 0 {
		return fmt.Errorf("安装包损坏：内嵌程序为空（请重新运行 release.ps1 构建）")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建安装目录：%w", err)
	}
	appPath := filepath.Join(dir, appExeName)
	if err := writeFileAtomic(appPath, appBinary); err != nil {
		return fmt.Errorf("写入应用文件：%w", err)
	}

	// 复制安装器自身到目标目录，作为卸载入口（与 UninstallString 对应）
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位安装器：%w", err)
	}
	setupPath := filepath.Join(dir, setupCopyName)
	if !samePath(self, setupPath) {
		data, err := os.ReadFile(self)
		if err != nil {
			return fmt.Errorf("读取安装器：%w", err)
		}
		if err := writeFileAtomic(setupPath, data); err != nil {
			return fmt.Errorf("写入卸载入口：%w", err)
		}
	}

	if err := createShortcut(shortcutPath(), appPath); err != nil {
		return fmt.Errorf("创建快捷方式：%w", err)
	}
	if err := writeUninstallEntry(dir, setupPath); err != nil {
		return fmt.Errorf("写入卸载注册项：%w", err)
	}
	return nil
}

// doUninstall 卸载：删快捷方式/注册项/应用文件，并调度安装目录延迟清理。
func doUninstall(dir string) error {
	if err := os.Remove(shortcutPath()); err != nil && !os.IsNotExist(err) {
		// 快捷方式删不掉不阻断卸载（可能已被手动删除或权限受限），但仍要可见
		fmt.Fprintf(os.Stderr, "警告：删除快捷方式失败：%v\n", err)
	}
	if err := registry.DeleteKey(registry.CURRENT_USER, uninstallKey); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("删除注册项：%w", err)
	}
	if err := os.Remove(filepath.Join(dir, appExeName)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除应用文件：%w", err)
	}
	// 卸载入口自身可能位于目录内，无法边运行边删除：交给独立进程延迟清理
	scheduleSelfCleanup(dir)
	return nil
}

// writeFileAtomic 同目录临时文件 + rename，避免写入中断留下半文件。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp_*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// shortcutPath 返回开始菜单快捷方式路径。
func shortcutPath() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = os.TempDir()
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", appName+".lnk")
}

// createShortcut 经 WScript.Shell 创建 .lnk（Go 标准库无 COM 能力）。
func createShortcut(lnk, target string) error {
	ps := fmt.Sprintf(
		`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.Save()`,
		lnk, target, filepath.Dir(target))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

// writeUninstallEntry 写 HKCU 卸载注册项（"应用和功能"可见）。
func writeUninstallEntry(dir, setupPath string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKey, registry.WRITE)
	if err != nil {
		return err
	}
	defer k.Close()
	values := map[string]string{
		"DisplayName":     appName,
		"DisplayVersion":  version,
		"InstallLocation": dir,
		"UninstallString": fmt.Sprintf(`"%s" -uninstall`, setupPath),
		"Publisher":       "tiancode",
	}
	for name, val := range values {
		if err := k.SetStringValue(name, val); err != nil {
			return err
		}
	}
	return nil
}

// scheduleSelfCleanup 派生独立进程延迟删除安装目录（规避"运行中无法自删"）。
func scheduleSelfCleanup(dir string) {
	ps := fmt.Sprintf(
		`Start-Sleep -Milliseconds 900; Remove-Item -Recurse -Force -ErrorAction SilentlyContinue '%s'`,
		dir)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "警告：调度目录清理失败，请手动删除 %s：%v\n", dir, err)
	}
}

// samePath 判断两路径是否指向同一位置（Windows 大小写不敏感）。
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
