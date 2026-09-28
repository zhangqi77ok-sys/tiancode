//go:build windows

package mcpclient

import (
	"os/exec"
	"strconv"
	"syscall"
)

// createNoWindow 是 Windows 的 CREATE_NO_WINDOW。
// 桌面进程没有控制台；Go 启动 .cmd 会转成 cmd.exe /c，不加这个标志就会弹出黑色命令行窗口。
const createNoWindow = 0x08000000

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
		HideWindow:    true,
	}
}

// killProcessTree 终止整棵进程树（taskkill /T 连子带孙）。
// 为什么必须按树杀：MCP 服务器是批处理链——我们拉起的是 cmd.exe（跑 npx.cmd），
// 它再派生 node.exe，node 再派生真正的 server 进程；只杀第一个 cmd 会留下一串无主的
// node.exe（实测：关掉应用/中断之后仍有 node 在跑，且下次启动再拉一份）。
func killProcessTree(pid int) error {
	k := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	k.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
	return k.Run()
}
