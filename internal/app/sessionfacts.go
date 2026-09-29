package app

import (
	"fmt"
	"runtime"
)

// sessionFacts 是每步系统说明的三行环境事实（0.0.06 用户要求）。
//
// 为什么只有三行、为什么必须在系统说明里：模型最大的浪费是把 Windows 命令
// 写成 PowerShell 语法、或对着错误的工作区猜路径——这些是"环境事实"，不是
// 行为守则，三行讲完。随会话根变化（prefaceFn 每步实时取），不写入账本，
// 绝不包含任何密钥（渠道凭证从不进入这里）。
func sessionFacts(root string) string {
	var osName string // switch 全分支覆盖，初值给 "" 即可（golangci-lint ineffassign）
	switch runtime.GOOS {
	case "windows":
		osName = "Windows"
	case "darwin":
		osName = "macOS"
	default:
		osName = "Linux"
	}
	shell := "sh"
	if runtime.GOOS == "windows" {
		// 实际 shell 是 cmd.exe（shelltool.newCommand 用 cmd /d /s /c 执行）：
		// 必须写明"不是 PowerShell"——模型曾把 Get-ChildItem 喂给 cmd（0.2.4 实测）
		shell = "cmd.exe"
	}
	if root == "" {
		return fmt.Sprintf("## 运行环境（事实，非守则）\n- 操作系统：%s\n- Shell：%s\n- 本会话工作区：无——纯对话，没有本地文件工具", osName, shell)
	}
	return fmt.Sprintf("## 运行环境（事实，非守则）\n- 操作系统：%s\n- Shell：%s\n- 本会话工作区：%s（本地文件工具的根，相对路径都相对它）", osName, shell, root)
}
