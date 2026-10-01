// browser_service 是浏览器驾驶舱的用例层：截图读取。
// 落盘在 internal/platform/browsertool（工具动作时写），这里只负责"按相对路径
// 读回来给前端"——路径校验是安全边界，必须放在数据根目录的知情方（编排层）。
package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tiancode/internal/core/tools"
	"tiancode/internal/platform/browsertool"
)

// browserNavTimeout 是驾驶舱单次导航的编排层总闸。工具内部单动作 45s + 截图
// 15s 的预算叠加可达 60s，这里收紧为 50s——打不开就报错，UI 不该等更久；
// 真正的立即中断仍靠会话级 ctx 取消（DeleteSession），超时只是兜底。
const browserNavTimeout = 50 * time.Second

// shotRoot 返回浏览器截图根目录。与 ChatService 装配时传给 browsertool.Pool 的
// 落盘根是同一取法（browsertool.ShotRootUnder），相对路径才有同一基准。
func (s *ChatService) shotRoot() string { return browsertool.ShotRootUnder(s.cfg.DataDir) }

// ReadBrowserShot 读取 browser 工具的会话截图（驾驶舱）：relPath 是工具结果里
// 给出的 browser-shots 根下相对路径（如 "s-1/shot-0001.png"），返回 base64 编码
// 的图片字节（前端 data URL 直用）。
// 安全边界：只允许根目录内的文件——Clean 规范化后做前缀校验，`..`、绝对路径
// 一律拒绝。截图路径经 IPC 暴露给前端，穿越面必须关死；命中不了（越界/缺失/
// 是目录）显式报错，绝不用空串或占位图冒充。
func (s *ChatService) ReadBrowserShot(relPath string) (string, error) {
	root := s.shotRoot()
	clean := filepath.Clean(filepath.FromSlash(relPath))
	if filepath.IsAbs(clean) || clean == "." {
		return "", fmt.Errorf("截图路径必须是 browser-shots 下的相对路径：%q", relPath)
	}
	abs := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("截图路径越界（必须在 browser-shots 内）：%q", relPath)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("读取截图失败 %s：%w", clean, err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// BrowserView 是用户点对话里的网址 → 右侧驾驶舱打开的返回载荷。字段与 chat:tool
// 事件的驾驶舱字段同源同义（shot 是 browser-shots 相对路径，前端 ReadBrowserShot
// 读图）；Output/Title 让前端合成一张与模型工具卡同构的本地卡——面板数据源保持
// 唯一（会话缓冲派生），不为用户浏览单开第二份状态。
type BrowserView struct {
	Shot    string   `json:"shot"`
	URL     string   `json:"url"`
	Console []string `json:"console"`
	Output  string   `json:"output"`
	Title   string   `json:"title"`
}

// BrowserNavigate 用该会话的浏览器 tab 打开网址并截图（右侧驾驶舱）。
// 为什么与模型共用同一工具端口：驾驶舱的立身之本是"所见即所控"——用户与模型
// 看同一个 tab，谁操作都算数；不写账本、不进模型上下文，浏览行为不是对话回合。
// 仅接受 http/https（javascript: 等危险 scheme 在入口拒绝，不给工具层兜底的机会）；
// 执行失败/业务失败显式报错，前端据此回退系统浏览器。
// ctx 取会话级 ctx（第二轮体检 R2）叠加单次导航总闸：DeleteSession 取消会话
// ctx 时在途导航立即中断——此前用 Background，open 45s + 截图 15s 不可取消，
// 还持有 tab 锁，同步的 DeleteSession 会被卡死 UI 最长 60 秒。
func (s *ChatService) BrowserNavigate(sessionID, rawURL string) (BrowserView, error) {
	if u, err := url.Parse(rawURL); err != nil || !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return BrowserView{}, fmt.Errorf("仅支持 http/https 链接：%q", rawURL)
	}
	// 存活校验必须在 sessionWorkspace（会经 ledgerFor 重开账本、给已删除会话
	// 重建空文件）之前：权威判据是账本文件还在盘上。
	if !s.sessionLedgerOnDisk(sessionID) {
		return BrowserView{}, fmt.Errorf("会话不存在或已删除：%s", sessionID)
	}
	st, err := s.ensureSessionTools(sessionID, s.sessionWorkspace(sessionID))
	if err != nil {
		return BrowserView{}, err
	}
	ctx, cancel := context.WithTimeout(st.ctx, browserNavTimeout)
	defer cancel()
	return browserNavigateWith(ctx, st.browser, rawURL)
}

// browserNavigateWith 是 BrowserNavigate 的执行内核：参数组装、结果与驾驶舱
// 字段映射。收口成 tools.ToolPort 参数便于契约测试（不真开浏览器）。
func browserNavigateWith(ctx context.Context, tab tools.ToolPort, rawURL string) (BrowserView, error) {
	args, err := json.Marshal(map[string]string{"action": "open", "url": rawURL})
	if err != nil {
		return BrowserView{}, fmt.Errorf("组装 open 参数失败：%w", err)
	}
	res, err := tab.Execute(ctx, args)
	if err != nil {
		return BrowserView{}, fmt.Errorf("打开网页失败：%w", err)
	}
	if res.IsError {
		return BrowserView{}, fmt.Errorf("打开网页失败：%s", res.Content)
	}
	view := BrowserView{Output: res.Content, Title: res.Title}
	if res.Visual != nil {
		view.Shot, view.URL, view.Console = res.Visual.Shot, res.Visual.URL, res.Visual.Console
	}
	if view.URL == "" {
		view.URL = rawURL
	}
	return view, nil
}
