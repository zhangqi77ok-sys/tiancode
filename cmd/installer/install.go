//go:build windows

package main

import (
	"errors"
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

// errAppRunning 表示应用正在运行、本次安装/卸载被中止，等待用户自行退出。
// 用哨兵错误而非普通错误：调用点据此选标题（这不是"安装失败"，而是"需要用户先操作"）。
var errAppRunning = errors.New("请先在系统托盘图标上点右键 →「退出」，等 tiancode 完全退出后，再运行本安装包。\n\n" +
	"安装器不会替你强制结束进程：强杀会跳过应用的正常收尾，可能丢失正在进行的会话。")

// appExitWaitTimeout 是静默模式下等待应用自行退出的上限。
// 为什么给 5s：自更新路径（app/update.go）先拉起安装器、再异步 Quit，
// 安装器必须等这段自然退出，而不是替它关窗（见 resolveAppClosePolicy）。
const appExitWaitTimeout = 5 * time.Second

// appClosePolicy 描述「应用正在运行」时安装器的处置。
type appClosePolicy struct {
	wait    time.Duration // 等应用自行退出的时长；0 = 不等
	blocked error         // 非 nil = 中止本次操作
}

// resolveAppClosePolicy 是纯函数，锁定"检测到应用在跑"时各模式下的处置。
//
// 为什么安装器不再自己关应用（0.0.24 关窗语义变更后的硬约束）：
// WM_CLOSE 在 winc 里被完全截断（winc/wndproc.go:87 → wails Quit() → OnBeforeClose），
// 而托盘版 OnBeforeClose 对一切关闭都返回 true（隐藏到托盘、进程存活，见 main.go）。
// 因此旧实现"先温和 taskkill 再强杀"的温和阶段**必然无效**，2s 之后只剩 /F 一条路：
// OnShutdown 不执行 → chat.Close() 不跑 → 孤儿 MCP node 进程、账本句柄不释放，
// 正在写入的 JSONL 可能被截断。修复方向由"安装器代替用户强杀"改为
// "交互模式请用户自己退出、静默模式等它自然退出"。
func resolveAppClosePolicy(running, quiet bool) appClosePolicy {
	switch {
	case !running:
		return appClosePolicy{}
	case quiet:
		// 静默模式只有自更新一条调用路径：应用已置退出标记（app/update.go:62），
		// 会自行 Quit。这里只等；等不到就中止，不替用户杀进程。
		return appClosePolicy{wait: appExitWaitTimeout}
	default:
		return appClosePolicy{blocked: errAppRunning}
	}
}

// ensureAppClosed 在动目标目录之前处置"应用正在运行"。
// 返回 nil 表示可以继续；返回 errAppRunning 时中止，并把可执行的中文指引带给用户。
func ensureAppClosed(quiet bool) error {
	p := resolveAppClosePolicy(isAppRunning(), quiet)
	if p.blocked != nil {
		appendSetupLog("中止：检测到 tiancode 正在运行，等待用户从托盘退出")
		return p.blocked
	}
	if p.wait == 0 || waitAppExit(p.wait) {
		return nil
	}
	appendSetupLog(fmt.Sprintf("中止：等待 tiancode 退出超过 %s，未改动任何文件", p.wait))
	return fmt.Errorf("等待 tiancode 退出超过 %s，已中止，未改动任何文件", p.wait)
}

// isAppRunning 用 tasklist 查询应用进程是否存在。查询失败一律视为未运行：
// 关闭是尽力而为的前置优化，探测失败不应让安装失败。
func isAppRunning() bool {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq "+appExeName, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false
	}
	return tasklistReportsApp(string(out))
}

// tasklistReportsApp 解析 tasklist 输出判断目标进程是否在列（纯函数，可单测）。
// 无匹配时 tasklist 输出本地化提示行（"信息: ..."/"INFO: ..."）且不含镜像名，
// 以「输出中是否出现 tiancode.exe」为判据，天然兼容各语言系统。
func tasklistReportsApp(out string) bool {
	return strings.Contains(strings.ToLower(out), strings.ToLower(appExeName))
}

// waitAppExit 轮询等待应用退出（时序判据见 docs/TESTING.md：轮询而非固定 sleep）。
//
// 仅用于静默模式下等待应用**自然**退出（自更新路径）。刻意不提供"强制结束"：
// 见 resolveAppClosePolicy —— 托盘语义下温和关闭必然无效，强杀会丢数据。
func waitAppExit(timeout time.Duration) bool {
	for deadline := time.Now().Add(timeout); ; {
		if !isAppRunning() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// doInstall 及其步骤在 steps.go：安装步骤编排与失败回滚放在一起，便于对照"做了什么 / 撤销什么"。

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
