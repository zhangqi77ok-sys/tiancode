package app

import "tiancode/internal/app"

// 浏览器驾驶舱绑定：把 browser 工具的截图读给前端（Wails 自动暴露）。

// ReadBrowserShot 读取 browser 工具的会话截图（驾驶舱）：relPath 是工具结果
// （chat:tool 事件的 shot 字段）给出的 browser-shots 根下相对路径（正斜杠，
// 如 "s-1/shot-0001.png"），返回 base64 编码的图片字节——前端拼
// data:image/png;base64,<返回值> 直接进 <img>。
// 只允许 browser-shots 目录内的文件（Clean + 前缀校验防穿越，校验在
// ChatService.ReadBrowserShot）；越界/缺失显式报错，壳层只转发不吞错。
func (b *Bind) ReadBrowserShot(relPath string) (string, error) {
	return b.chat.ReadBrowserShot(relPath)
}

// BrowserNavigate 用户点对话里的网址 → 会话浏览器打开（右侧驾驶舱）。
// 语义在 ChatService.BrowserNavigate：与模型共用同一 tab（所见即所控），
// 仅 http/https；失败显式报错，前端回退系统浏览器。壳层只转发。
func (b *Bind) BrowserNavigate(sessionID, rawURL string) (app.BrowserView, error) {
	return b.chat.BrowserNavigate(sessionID, rawURL)
}
