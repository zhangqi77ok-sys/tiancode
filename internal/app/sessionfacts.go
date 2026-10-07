package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"tiancode/internal/core/tools"
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
	return fmt.Sprintf("## 运行环境（事实，非守则）\n- 操作系统：%s\n- Shell：%s\n- 本会话工作区：%s（本地文件工具的根，相对路径都相对它）", osName, shell, root) +
		agentsRulesSection(root)
}

// agentsRulesLimit 是注入的项目规则正文字节上限：AGENTS.md 写多长是项目的事，
// 系统说明不能被它无限撑大（超出头尾保留，注明节选）。
const agentsRulesLimit = 8 << 10

// agentsRulesSection 读取工作区根的 AGENTS.md 作为项目规则注入（0.0.42）：
// 项目特有的构建命令、代码风格、禁区由项目自己声明，不再靠用户每轮口述。
// 纪律：每轮读一次快照进本轮 preface（回合内文件变更下一轮生效）——文本随
// 文件内容固定，逐字稳定不破坏 prompt cache；没有该文件 = 整段不出现（零噪声）；
// 读取失败（存在但读不了）忽略——项目规则是增强，不该为此打断对话。
func agentsRulesSection(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(b))
	if content == "" {
		return ""
	}
	if len(content) > agentsRulesLimit {
		content = tools.HeadTail(content, agentsRulesLimit) + "\n（超出上限，已节选；全文见工作区根 AGENTS.md）"
	}
	return "\n\n## 项目规则（来自工作区根 AGENTS.md，本会话开始时快照）\n\n" + content
}
