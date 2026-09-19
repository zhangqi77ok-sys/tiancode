package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

//go:embed assets/tiancode.exe
var tiancodeBinary []byte

//go:embed assets/uninstall.exe
var uninstallerBinary []byte

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

const (
	MB_OK          = 0x00000000
	MB_OKCANCEL    = 0x00000001
	MB_YESNOCANCEL = 0x00000003
	MB_YESNO       = 0x00000004
	MB_ICONQUESTION = 0x00000020
	MB_ICONINFO     = 0x00000040
	MB_ICONERROR    = 0x00000010
	IDOK           = 1
	IDCANCEL       = 2
	IDYES          = 6
	IDNO           = 7
)

func messageBox(title, text string, style uint) int {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	r, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), uintptr(style))
	return int(r)
}

// chooseFolderDialog 调用系统原生资源管理器文件夹选择框 (通过 WinForms 唤起，注入 CREATE_NO_WINDOW 无黑框)
func chooseFolderDialog(initialDir string) (string, error) {
	psScript := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
$f = New-Object System.Windows.Forms.FolderBrowserDialog
$f.Description = "请选择 Tcode Agentic Studio 的安装目标目录"
$f.ShowNewFolderButton = $true
if (Test-Path "%s") { $f.SelectedPath = "%s" }
$res = $f.ShowDialog()
if ($res -eq [System.Windows.Forms.DialogResult]::OK) {
	[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
	Write-Output $f.SelectedPath
}
`, initialDir, initialDir)

	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	selected := strings.TrimSpace(string(out))
	return selected, nil
}

func main() {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		home, _ := os.UserHomeDir()
		localAppData = filepath.Join(home, "AppData", "Local")
	}

	defaultInstallDir := filepath.Join(localAppData, "Programs", "Tiancode")
	customDir := ""
	isSilent := false
	isTestingMode := false

	// 解析命令行参数: 支持 -silent, /S, -dir <path>, --dir <path>, /D=<path>, -dir=<path>, --silent-install-dir
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "-silent" || arg == "/S" || arg == "/s" || arg == "--silent" {
			isSilent = true
		} else if arg == "--silent-install-dir" {
			isSilent = true
			isTestingMode = true
			if i+1 < len(os.Args) {
				customDir = os.Args[i+1]
				i++
			}
		} else if arg == "-dir" || arg == "--dir" || arg == "-d" {
			if i+1 < len(os.Args) {
				customDir = os.Args[i+1]
				i++
			}
		} else if strings.HasPrefix(arg, "/D=") {
			customDir = strings.TrimPrefix(arg, "/D=")
		} else if strings.HasPrefix(arg, "-dir=") {
			customDir = strings.TrimPrefix(arg, "-dir=")
		} else if strings.HasPrefix(arg, "--dir=") {
			customDir = strings.TrimPrefix(arg, "--dir=")
		} else if strings.HasPrefix(arg, "--silent-install-dir=") {
			isSilent = true
			isTestingMode = true
			customDir = strings.TrimPrefix(arg, "--silent-install-dir=")
		}
	}

	installDir := defaultInstallDir
	if customDir != "" {
		cleaned := filepath.Clean(customDir)
		if !strings.EqualFold(filepath.Base(cleaned), "Tiancode") {
			installDir = filepath.Join(cleaned, "Tiancode")
		} else {
			installDir = cleaned
		}
	}

	if !isSilent {
		// 若命令行未指定自定义路径，提供选择默认路径与自定义浏览选项
		if customDir == "" {
			welcomeText := fmt.Sprintf(
				"欢迎使用 湉码 / tiancode 安装向导！\n\n"+
					"系统推荐默认安装位置：\n%s\n\n"+
					"• 点击【是 (Y)】：直接使用推荐默认路径快速安装\n"+
					"• 点击【否 (N)】：自定义浏览选择安装文件夹\n"+
					"• 点击【取消】：退出安装向导\n\n"+
					"安装程序将自动创建桌面快捷方式并注册系统卸载项。",
				defaultInstallDir,
			)

			ans := messageBox("湉码 v0.0.1 安装向导", welcomeText, MB_YESNOCANCEL|MB_ICONQUESTION)
			if ans == IDCANCEL {
				return
			} else if ans == IDNO {
				// 用户选择自定义文件夹
				selected, err := chooseFolderDialog(defaultInstallDir)
				if err != nil || selected == "" {
					// 用户未选择或取消，直接退出
					return
				}

				// 若选中的文件夹尾部未包含 Tiancode，自动创建专用子目录
				selectedClean := filepath.Clean(selected)
				if !strings.EqualFold(filepath.Base(selectedClean), "Tiancode") {
					installDir = filepath.Join(selectedClean, "Tiancode")
				} else {
					installDir = selectedClean
				}

				confirmText := fmt.Sprintf(
					"您已选定自定义安装目录：\n%s\n\n是否立即开始安装？",
					installDir,
				)
				if messageBox("确认安装目录", confirmText, MB_YESNO|MB_ICONQUESTION) != IDYES {
					return
				}
			}
		} else {
			// 命令行已传入 customDir 且为交互模式，弹窗确认即可
			confirmText := fmt.Sprintf(
				"湉码 安装向导将安装至指定目录：\n%s\n\n是否立即开始安装？",
				installDir,
			)
			if messageBox("确认安装目录", confirmText, MB_YESNO|MB_ICONQUESTION) != IDYES {
				return
			}
		}
	}

	targetExe := filepath.Join(installDir, "tiancode.exe")
	uninstallExe := filepath.Join(installDir, "uninstall.exe")

	// 1. 终止旧进程 (仅在非测试模式下执行，避免单元/探活测试误杀正常运行的 Tcode 实例；带 /T 树杀与 0x08000000 零黑框防护)
	if !isTestingMode {
		for _, im := range []string{"tiancode.exe", "tcode.exe"} {
			killCmd := exec.Command("taskkill", "/F", "/T", "/IM", im)
			killCmd.SysProcAttr = &syscall.SysProcAttr{
				CreationFlags: 0x08000000,
				HideWindow:    true,
			}
			_ = killCmd.Run()
		}
	}

	// 2. 创建安装目录
	if err := os.MkdirAll(installDir, 0755); err != nil {
		messageBox("安装失败", fmt.Sprintf("无法创建安装目录: %v", err), MB_ICONERROR)
		return
	}

	// 3. 释放主体 tiancode.exe
	_ = os.Remove(targetExe)
	if err := os.WriteFile(targetExe, tiancodeBinary, 0755); err != nil {
		messageBox("安装失败", fmt.Sprintf("无法写入应用程序文件: %v", err), MB_ICONERROR)
		return
	}

	// 4. 释放卸载程序 uninstall.exe
	_ = os.Remove(uninstallExe)
	if err := os.WriteFile(uninstallExe, uninstallerBinary, 0755); err != nil {
		messageBox("安装失败", fmt.Sprintf("无法写入卸载程序文件: %v", err), MB_ICONERROR)
		return
	}

	// 5. 创建快捷方式 (桌面 + 开始菜单) - 仅在非隔离测试模式下生成
	if !isTestingMode {
		homeDir, _ := os.UserHomeDir()
		desktopLnk := filepath.Join(homeDir, "Desktop", "湉码.lnk")
		startMenuDir := filepath.Join(homeDir, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs")
		startMenuLnk := filepath.Join(startMenuDir, "湉码.lnk")

		createShortcut(desktopLnk, targetExe, installDir)
		createShortcut(startMenuLnk, targetExe, installDir)

		// 6. 注册 Windows 卸载表项
		regPath := `Software\Microsoft\Windows\CurrentVersion\Uninstall\Tiancode`
		k, _, err := registry.CreateKey(registry.CURRENT_USER, regPath, registry.ALL_ACCESS)
		if err == nil {
			defer k.Close()
			_ = k.SetStringValue("DisplayName", "湉码 tiancode v0.0.1")
			_ = k.SetStringValue("DisplayVersion", "0.0.1")
			_ = k.SetStringValue("Publisher", "tiancode")
			_ = k.SetStringValue("DisplayIcon", targetExe+",0")
			_ = k.SetStringValue("InstallLocation", installDir)
			_ = k.SetStringValue("UninstallString", fmt.Sprintf("\"%s\"", uninstallExe))
			_ = k.SetDWordValue("NoModify", 1)
			_ = k.SetDWordValue("NoRepair", 1)
		}
	}

	// 7. 安装完成提示与直接启动选项
	if !isSilent {
		launchAns := messageBox(
			"安装成功",
			fmt.Sprintf("✓ 湉码 v0.0.1 已成功安装到：\n%s\n\n桌面与开始菜单已生成快捷方式。\n\n是否立即启动应用程序？", installDir),
			MB_YESNO|MB_ICONINFO,
		)

		if launchAns == IDYES {
			cmd := exec.Command(targetExe)
			cmd.Dir = installDir
			_ = cmd.Start()
		}
	}
}

func escapePsSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func createShortcut(lnkPath, targetPath, workDir string) {
	psScript := fmt.Sprintf(
		`$ws = New-Object -ComObject WScript.Shell; $s = $ws.CreateShortcut('%s'); $s.TargetPath = '%s'; $s.WorkingDirectory = '%s'; $s.IconLocation = '%s,0'; $s.Save()`,
		escapePsSingleQuote(lnkPath),
		escapePsSingleQuote(targetPath),
		escapePsSingleQuote(workDir),
		escapePsSingleQuote(targetPath),
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	_ = cmd.Run()
}
