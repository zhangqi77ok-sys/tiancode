// 系统通知（0.0.43）：Windows toast，零新依赖（PowerShell 调 WinRT）。
//
// 做什么：SendNotification 是前端触发的绑定端点——前端自己知道窗口是否失焦
// （document.hasFocus，后端无"前台会话"概念），只在失焦时对 轮次终态 / 等待审批 /
// 等待答复 三类事件调用。后台长任务跑完不用切回来盯梢。
// 纪律：尽力而为——通知失败只返回错误给前端记日志，绝不影响对话本身；
// 全局限速防连发（连续终态/审批合并成一条）。
package app

import (
	"encoding/xml"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// notifyRateLimit 是两条通知之间的最小间隔：批量终态/连续审批只弹一条，
// 防止"跑完 5 个子任务弹 5 个 toast"。
const notifyRateLimit = 3 * time.Second

// notifyTitleLimit / notifyBodyLimit 是文本上限：toast 版面有限，超长截断。
const (
	notifyTitleLimit = 80
	notifyBodyLimit  = 200
)

var (
	notifyMu       sync.Mutex
	notifyLastSent time.Time
)

// notifyRunner 是唯一启动进程的出口（测试注入）；隐藏控制台防黑窗闪烁。
var notifyRunner = func(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd.Run()
}

// SendNotification 弹一条系统 toast（前端在窗口失焦时调用）。
// 参数有界、全局限速；失败错误上抛（前端记日志，不影响对话）。
func (b *Bind) SendNotification(title, body string) error {
	title = clampNotifyText(title, notifyTitleLimit)
	body = clampNotifyText(body, notifyBodyLimit)
	if title == "" && body == "" {
		return fmt.Errorf("通知标题与正文都为空")
	}
	notifyMu.Lock()
	if time.Since(notifyLastSent) < notifyRateLimit {
		notifyMu.Unlock()
		return nil // 限速窗口内：静默丢弃（连发保护，不是错误）
	}
	notifyLastSent = time.Now()
	notifyMu.Unlock()

	ps := buildToastScript(title, body)
	return notifyRunner("powershell", []string{"-NoProfile", "-NonInteractive", "-Command", ps})
}

// clampNotifyText 归一空白并按字节截断（回退到 UTF-8 边界，不切出非法序列）。
func clampNotifyText(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= limit {
		return s
	}
	cut := s[:limit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "…"
}

// buildToastScript 生成 PowerShell toast 命令（ToastText02：标题加粗 + 正文）。
// 文本经 XML 转义——toast 内容是 XML，直接拼接会被注入破坏结构。
func buildToastScript(title, body string) string {
	t, _ := xmlEscape(title)
	bd, _ := xmlEscape(body)
	return `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null; ` +
		`$x=[Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02); ` +
		`$t=$x.GetElementsByTagName('text'); ` +
		`$t.Item(0).AppendChild($x.CreateTextNode('` + t + `'))|Out-Null; ` +
		`$t.Item(1).AppendChild($x.CreateTextNode('` + bd + `'))|Out-Null; ` +
		`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('tiancode').Show([Windows.UI.Notifications.ToastNotification]::new($x))`
}

// xmlEscape 把文本安全地嵌入单引号包裹的 XML 文本节点（PowerShell 单引号串里
// 再出现单引号会断串，所以双重处理：XML 实体化 + 单引号翻倍）。
func xmlEscape(s string) (string, error) {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return "", err
	}
	return strings.ReplaceAll(b.String(), "'", "''"), nil
}
