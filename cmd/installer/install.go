//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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

// warn 报告非致命问题（跳过某项、清理失败等），并保证"可见"。
//
// 为什么必须同时落盘：安装器以 -H windowsgui 构建，是 GUI 子系统程序——没有控制台，
// 写到 stderr 的警告**用户永远看不到**（-quiet 脚本化安装更是如此）。只写 stderr 等于
// 静默降级，而本项目的纪律是"不允许静默降级"。
func warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "警告：%s\n", msg)
	appendSetupLog("警告：" + msg)
}

// setupLogPath 返回安装器日志路径（%APPDATA%\tiancode\setup.log），
// 与应用数据目录一致（ADR-0006），便于出问题时一处排查。
func setupLogPath() string {
	base := os.Getenv("APPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, appName, "setup.log")
}

// appendSetupLog 追加一行时间戳日志。失败静默——日志不可写不应影响安装本身，
// 但每条调用点本身都已另有可见通道（stderr 或对话框）。
func appendSetupLog(line string) {
	base := os.Getenv("APPDATA")
	if base == "" {
		return
	}
	dir := filepath.Join(base, appName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(setupLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), line)
}

// doInstall 安装：写应用 exe → 复制自身（卸载入口）→ 快捷方式 → 注册表项。
// desktopIcon 为 false 时跳过桌面快捷方式（仅开始菜单），供受限环境或脚本化部署选择。
func doInstall(dir string, desktopIcon bool) error {
	appendSetupLog(fmt.Sprintf("install v%s dir=%s desktopIcon=%v", version, dir, desktopIcon))
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

	for _, s := range installShortcuts(desktopIcon) {
		if err := createShortcut(s.lnk, appPath, s.workDir); err != nil {
			if s.required {
				return fmt.Errorf("创建快捷方式 %s：%w", filepath.Base(s.lnk), err)
			}
			// 便利项失败不中断安装（应用与卸载项均可就位），但必须可见：
			// 静默降级会让用户事后才发现入口缺失，而那时已无从判断原因。
			warn("未创建快捷方式 %s：%v", filepath.Base(s.lnk), err)
		}
	}
	if err := writeUninstallEntry(dir, setupPath); err != nil {
		return fmt.Errorf("写入卸载注册项：%w", err)
	}
	return nil
}

// doUninstall 卸载：删快捷方式/注册项/应用文件，并调度安装目录延迟清理。
func doUninstall(dir string) error {
	appendSetupLog(fmt.Sprintf("uninstall dir=%s", dir))
	// 卸载始终按「全部快捷方式」清理（不区分安装时是否跳过桌面项）：
	// 删除不存在的 .lnk 是幂等的，而漏删会在用户桌面留下孤儿入口。
	for _, lnk := range uninstallShortcuts() {
		if err := os.Remove(lnk); err != nil && !os.IsNotExist(err) {
			// 快捷方式删不掉不阻断卸载（可能已被手动删除或权限受限），但仍要可见
			warn("删除快捷方式 %s 失败：%v", filepath.Base(lnk), err)
		}
	}
	// 注册表键名是固定的（"应用和功能"里每个应用一项），因此它可能属于**另一处安装**。
	// 只有当它记录的 InstallLocation 就是本次要卸载的目录时才可删除，否则会把别的
	// 安装（例如用户正式装的那一份）从"应用和功能"里静默抹掉——实测踩过：
	// 用隔离目录做卸载验证，连带删掉了正式安装的注册项。
	if ownsUninstallEntry(dir) {
		if err := registry.DeleteKey(registry.CURRENT_USER, uninstallKey); err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("删除注册项：%w", err)
		}
	} else {
		warn("注册项不属于 %s，已跳过删除（避免影响其它安装）", dir)
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

// shortcut 描述一个待创建的 .lnk。
// required 表示创建失败是否应中断安装：开始菜单是文档化的启动入口，必须成功；
// 桌面快捷方式是便利项，失败只告警。
type shortcut struct {
	lnk      string
	workDir  string
	required bool
}

// installShortcuts 返回安装时需要创建的全部快捷方式，顺序固定（开始菜单、桌面）。
//
// 顺序固定、集合与 uninstallShortcuts 逐项对应，由 install_test.go 锁定：
// 卸载漏删任何一个都会在用户开始菜单/桌面留下指向已删程序的孤儿 .lnk。
func installShortcuts(desktopIcon bool) []shortcut {
	out := []shortcut{{lnk: startMenuShortcutPath(), workDir: userHome(), required: true}}
	if desktopIcon {
		out = append(out, shortcut{lnk: desktopShortcutPath(), workDir: userHome(), required: false})
	}
	return out
}

// uninstallShortcuts 返回卸载时需要删除的全部快捷方式路径。
// 刻意不含条件分支：卸载一律两个都尝试删除（幂等），避免安装参数与卸载参数不一致时漏删。
func uninstallShortcuts() []string {
	return []string{startMenuShortcutPath(), desktopShortcutPath()}
}

// startMenuShortcutPath 返回开始菜单快捷方式路径。
func startMenuShortcutPath() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = os.TempDir()
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", appName+".lnk")
}

// desktopShortcutPath 返回桌面快捷方式路径。
// 文件名保持 ASCII 的 tiancode.lnk（与开始菜单一致），避免安装器出现多字节字面量。
func desktopShortcutPath() string {
	return filepath.Join(desktopDir(), appName+".lnk")
}

// desktopDir 返回桌面目录：优先注册表中的实际位置，读不到再回退。
// 为什么不能直接拼 %USERPROFILE%\Desktop：桌面可能被 OneDrive 或组策略重定向
// （此时真实桌面在 %USERPROFILE%\OneDrive\Desktop），硬编码会把快捷方式写到无人查看的位置。
func desktopDir() string {
	if p := shellDesktopDir(); p != "" {
		return p
	}
	return desktopDirFallback()
}

// desktopDirFallback 在注册表不可用时回退到 %USERPROFILE%\Desktop。
func desktopDirFallback() string {
	return filepath.Join(userHome(), "Desktop")
}

// shellDesktopDir 读 HKCU 的 User Shell Folders\Desktop（系统/OneDrive 重定向后的真实位置）。
// 任何失败都返回空串交由调用方回退：读不到注册表不应让安装失败或中断。
func shellDesktopDir() string {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\User Shell Folders`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	val, _, err := k.GetStringValue("Desktop")
	if err != nil || val == "" {
		return ""
	}
	return expandEnvVars(val)
}

// expandEnvVars 展开 Windows 风格的 %NAME% 环境变量引用。
// 为什么不用 os.ExpandEnv：它只处理 $NAME / ${NAME}，而注册表 Shell Folders 的值是
// Windows 的 %USERPROFILE% 形式——直接用会把字面量当路径。
func expandEnvVars(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i+1:], '%')
		if end < 0 { // 不成对的 %，按字面保留
			b.WriteString(s[i:])
			break
		}
		name := s[i+1 : i+1+end]
		if name == "" { // "%%" 视为字面 %
			b.WriteByte('%')
		} else {
			b.WriteString(os.Getenv(name))
		}
		i += end + 2
	}
	return b.String()
}

// userHome 返回用户主目录。
// 为什么快捷方式工作目录指向主目录而非安装目录：避免应用把运行时文件
// 写进安装目录（配置/会话已固定在 %APPDATA%\tiancode，见 ADR-0006）。
func userHome() string {
	if h := os.Getenv("USERPROFILE"); h != "" {
		return h
	}
	return os.TempDir()
}

// powershellExe 返回可用的 powershell.exe 路径。
//
// 为什么不直接写 "powershell.exe"：powershell.exe 并不位于 System32 目录本身，
// 而在 System32\WindowsPowerShell\v1.0\，通常靠 PATH 提供。依赖 PATH 会让安装器在
// 精简 PATH 的环境（CI、脚本化部署、部分受管终端）下直接失败——实测报错为
// `exec: "powershell.exe": executable file not found in %PATH%`，且失败发生在应用文件
// 已写入之后，会留下一个没有卸载注册项的坏安装。
func powershellExe() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	p := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return "powershell.exe" // 最后手段：仍交给 PATH 解析
}

// createShortcut 经 WScript.Shell 创建 .lnk（Go 标准库无 COM 能力）。
func createShortcut(lnk, target, workDir string) error {
	ps := fmt.Sprintf(
		`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.Save()`,
		lnk, target, workDir)
	cmd := exec.Command(powershellExe(), "-NoProfile", "-NonInteractive", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

// ownsUninstallEntry 判断共享卸载注册项是否属于本次要卸载的安装目录。
// 读不到键时返回 true（保持既有的"正常卸载"行为，不要让读取失败反而阻塞用户卸载）。
func ownsUninstallEntry(dir string) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	loc, _, err := k.GetStringValue("InstallLocation")
	if err != nil {
		return true
	}
	return uninstallOwnsEntry(loc, dir)
}

// uninstallOwnsEntry 判断注册项记录的安装位置是否指向本次卸载的目录。
// 空值视为"无从判断"，保守地允许删除（避免因注册表缺字段而让用户卸不掉）。
func uninstallOwnsEntry(recordedLoc, dir string) bool {
	if recordedLoc == "" {
		return true
	}
	return samePath(recordedLoc, dir)
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
	cmd := exec.Command(powershellExe(), "-NoProfile", "-NonInteractive", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
	if err := cmd.Start(); err != nil {
		warn("调度目录清理失败，请手动删除 %s：%v", dir, err)
	}
}

// samePath 判断两路径是否指向同一位置（Windows 大小写不敏感）。
func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
