// Package browsertool 内置浏览器工具：经 CDP 驱动本机 Edge/Chrome，给模型
// 零配置的「看本地前端」能力——打开页面、列出可交互元素（含无文本的图标按钮）、
// 点击、输入、下翻一屏、后退、读控制台报错。前端开发闭环：模型改完代码自己开
// 页面、看报错、自己修。
//
// 驾驶舱（0.0.28）：改变页面状态的动作完成后自动补一张当前视口截图，连同页面
// URL 与控制台尾部随工具结果流给前端（tools.ToolResult.Visual）——用户实时
// "看见"模型正在看的页面，不再只靠文字想象。截图落盘到数据目录的 browser-shots
// 下（按会话隔离、文件名可预测），前端经壳层 ReadBrowserShot 按相对路径读图。
//
// 生命周期：进程级共享浏览器（Pool，惰性启动、全应用共享）。headless 是进程级
// 开关，无头（默认）与有头（open 显式传 headless=false，调试用）各自惰性拉起
// 至多一个进程；每个会话一个 tab（Tool）——并行会话各自导航、各自控制台日志、
// 各自后退历史，互不串页面状态。会话删除时收 tab；浏览器进程与临时目录由
// ChatService.Close 统一收。
// 隐私与定位边界：无头 + 显式临时 user-data-dir——适合看自己改的本地页面与
// 报错，不适合替用户操作已登录的网站（看不到日常浏览器的登录态与历史）。
// 审批：本工具默认**在**审批清单（0.0.28）——browser 可提交表单，每个动作执行
// 前都经用户确认；不想要逐次确认可在设置里把它移出审批清单。
//
// 被谁依赖：internal/app（装配）。依赖谁：chromedp（纯 Go CDP 客户端——Go
// 标准库没有 CDP 实现，chromedp 是事实标准、无 CGO；这是新依赖的完整理由）。
package browsertool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"tiancode/internal/core/tools"
	"tiancode/internal/platform/applog"
	"tiancode/internal/platform/configfile"
)

const (
	actionTimeout   = 45 * time.Second // 单个动作上限（页面加载慢也要有界）
	launchTimeout   = 45 * time.Second // tab 首启（可能含浏览器进程冷启动）上限
	consoleRingSize = 50               // 控制台消息环形缓冲（每 tab 最近 N 条）
	maxElements     = 120              // snapshot 最多列出的元素数（控 token）
	maxTextRunes    = 60               // 单个元素文本截断（控 token）
	hydrateWait     = 1500 * time.Millisecond
	shotTimeout     = 15 * time.Second // 单张截图上限（截不到就报，不拖累主动作太久）
	maxShotBytes    = 1536 * 1024      // 截图落盘上限（约 1.5MB）：超限缩图/转 JPEG
	maxShotWidth    = 1280             // 缩图目标宽上限（等比）
	shotQuality     = 82               // JPEG 质量：截图"看得清"即可，不求印刷级
	consoleTailN    = 8                // 随结果流给前端的控制台尾部条数
	// shotKeepPerSession 是每会话保留的截图张数（第二轮体检 R5：截图只写不删曾让
	// browser-shots 无界增长）。驾驶舱永远只展示最新一张、模型回看也只看最近几帧，
	// 50 张 × ≤1.5MB ≈ 75MB/会话封顶——再旧的历史帧没有消费方，纯占磁盘。
	shotKeepPerSession = 50
)

// selJS 是"可交互元素"的统一定义：snapshot / click / fill 用**同一段选择器与顺序**，
// ref（下标）才稳定。顺序 = 文档序。
const selJS = `a[href],button,input,select,textarea,[role="button"],[contenteditable="true"]`

// emptySnapshot 是"一个元素都没有"的约定文本：open 靠它识别 SPA 水合场景做重试。
const emptySnapshot = "（页面上没有发现屏内可交互元素）"

// browserProc 是一种形态（headless=true 无头 / false 有头）的共享浏览器进程。
// headless 是进程级开关，同一进程里的 tab 形态一致——两种形态各自惰性拉起、
// 全应用各最多一个进程；tab 上下文全部派生自 allocCtx，cancel 即级联收掉。
type browserProc struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	tmpDir      string
}

// close 收进程与临时目录（幂等）。临时目录删除失败不吞：原样上抛，由调用方
// 按场景决定"阻断"（退出清理要可见）还是"带理由放行"（重建不该被残留挡住）。
func (bp *browserProc) close() error {
	if bp.allocCancel != nil {
		bp.allocCancel() // 级联收掉所有 tab 与浏览器进程
		bp.allocCancel = nil
	}
	if bp.tmpDir == "" {
		return nil
	}
	dir := bp.tmpDir
	bp.tmpDir = ""
	return os.RemoveAll(dir)
}

// Pool 是进程级共享浏览器（惰性启动）：无头/有头两种形态各最多一个进程。
// 每个会话通过 NewTab 拿独立 tab（独立 target/导航历史/控制台日志），并行会话
// 互不串页面状态；进程与临时目录由 ChatService.Close 统一收。
type Pool struct {
	mu       sync.Mutex
	shotRoot string                // 截图根目录（会话子目录放其下）
	procs    map[bool]*browserProc // key = headless（两种形态的进程记录）
}

// ShotRootUnder 返回指定数据目录下的截图根目录。ChatService（落盘）与壳层
// （ReadBrowserShot 读图）必须共用同一取法，相对路径才有同一基准。
func ShotRootUnder(dataDir string) string { return filepath.Join(dataDir, "browser-shots") }

// DefaultShotRoot 返回截图根目录（用户级数据目录下，与渠道配置、会话账本同源）。
func DefaultShotRoot() string { return ShotRootUnder(configfile.Dir()) }

// NewPool 构造共享浏览器池（截图根目录取用户级数据目录；惰性：首个 tab 首次
// open 才拉起进程）。
func NewPool() *Pool { return NewPoolAt(DefaultShotRoot()) }

// NewPoolAt 构造指定截图根目录的共享浏览器池（ChatService 传应用数据目录下的
// browser-shots；测试传临时目录）。
func NewPoolAt(shotRoot string) *Pool {
	return &Pool{shotRoot: shotRoot, procs: map[bool]*browserProc{}}
}

// NewTab 返回一个会话级浏览器 tab 句柄（惰性：首次 open 才建 tab/拉进程）。
// sessionID 用作截图子目录名（经 safeDirName 清洗，异常 ID 也引不出根目录）。
func (p *Pool) NewTab(sessionID string) *Tool {
	return &Tool{pool: p, sessionID: safeDirName(sessionID)}
}

// Close 收掉两种形态的浏览器进程与临时目录（应用退出时调用；幂等）。
// 多份清理错误合并上抛——退出路径上失败一个也要看得见，不能只报第一个。
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var errs []error
	for headless, bp := range p.procs {
		if err := bp.close(); err != nil {
			errs = append(errs, fmt.Errorf("收浏览器进程（headless=%v）：%w", headless, err))
		}
	}
	p.procs = map[bool]*browserProc{}
	return errors.Join(errs...)
}

// procFor 返回指定形态的浏览器进程（惰性拉起）。自持 p.mu：并行会话的首次 open
// 会并发进入这里（Tool.ensure 只持本 tab 的 mu，从不持 p.mu），共享的 p.procs
// 必须锁内读写——否则是数据竞争，最坏 "concurrent map writes" 直接崩；双进程
// 竞写 last-wins 还会泄漏一个浏览器进程与临时目录（Close 永远收不到）。同形态的
// 并发拉起随之在锁上串行化：每个形态全应用至多一个进程。锁内只做收残留与建
// allocator 上下文（真拉起进程发生在 ensure 的首次 Run），持锁时间有界。
// 上一次进程已被收掉或崩溃则重建——重建后旧 tab 全部失效，动作会报"浏览器进程
// 已退出"，模型重新 open 即可，不需要重启应用。
func (p *Pool) procFor(headless bool) (*browserProc, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if bp, ok := p.procs[headless]; ok {
		if bp.allocCtx.Err() == nil {
			return bp, nil
		}
		// 进程已退出：清残留（cancel 对已取消 ctx 是 no-op），允许重建。此刻临时
		// 目录多半已随进程消失；删除失败只记日志不挡重建——残留由系统临时目录
		// 清理兜底，而"重建浏览器"是用户正在等的主线。
		if err := bp.close(); err != nil {
			applog.Errorf("浏览器临时目录清理失败（残留由系统临时目录清理兜底）：%v", err)
		}
	}
	exe := FindExecPath()
	if exe == "" {
		return nil, fmt.Errorf("本机没有找到 Edge 或 Chrome（把可执行文件路径放进环境变量 TIANCODE_BROWSER 可指定）")
	}
	dir, err := os.MkdirTemp("", "tiancode-browser-")
	if err != nil {
		return nil, err
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(exe),
		chromedp.UserDataDir(dir),
		chromedp.Flag("headless", headless),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-gpu", true),
	)
	actx, acancel := chromedp.NewExecAllocator(context.Background(), opts...)
	bp := &browserProc{allocCtx: actx, allocCancel: acancel, tmpDir: dir}
	p.procs[headless] = bp
	return bp, nil
}

// Tool 是会话级浏览器 tab：一个会话一个实例，动作只作用于自己的 tab。
type Tool struct {
	pool      *Pool
	sessionID string          // 截图子目录名（NewTab 时已清洗；空会话 → "adhoc"）
	mu        sync.Mutex      // 本 tab 动作互斥（模型对同一会话的调用天然串行）
	headless  bool            // 当前 tab 所在进程的形态（open 换形态时据此重建）
	shotSeq   int             // 截图序号（会话内单调递增：文件名可预测、永不覆盖）
	tctx      context.Context // tab 上下文（首次 open 时创建；cancel = 关 tab）
	tcancel   context.CancelFunc
	logsMu    sync.Mutex // 控制台日志专用锁：事件回调不能等 mu（Execute 持 mu 阻塞在动作上）
	logs      []string   // 控制台/页面错误环形缓冲（本 tab 最近 consoleRingSize 条）
}

// Name 实现工具端口。
func (t *Tool) Name() string { return "browser" }

// Description 工具说明：定位是"看本地前端页面与报错"，明确不适合登录站。
// 开头两句是给模型的"路由宣告"（实证教训 2026-10-01）：不宣告唯一性，模型在
// "给用户看网页"时会按训练习惯去找 cursor-ide-browser 之类的 MCP 浏览器，
// 碰壁后误判"内置浏览器用不了"，绕道 shell 打开——用户就什么都看不到了。
func (t *Tool) Description() string {
	return "内置浏览器，本环境唯一的浏览器：打开任意网页（本地 dev server 如 http://127.0.0.1:8765 或公网 URL）并实时展示给用户（右侧驾驶舱面板），" +
		"列出当前屏内可交互元素（含无文本的图标按钮）、点击按钮、填写输入框、下翻一屏、后退、读取控制台报错、截取当前画面。" +
		"要让用户看到网页、或验证自己写的前端页面，必须用本工具；不要用 shell start 打开网页，也不要经 mcp 找浏览器（本环境没有 cursor-ide-browser / playwright 这类 MCP 浏览器）。" +
		"流程：先 open(url)，再 snapshot 拿元素 ref，然后按 ref 执行 click/fill；长页面用 scroll 下翻看更多元素。" +
		"注意：默认无头模式 + 独立临时配置目录，看不到用户日常浏览器的登录态，不要用它操作需要登录的网站；" +
		"fill/click 可能提交数据，browser 默认在审批清单（每个动作执行前需用户确认）。"
}

// Schema 动作参数定义。
func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": ["open", "snapshot", "scroll", "click", "fill", "back", "console", "screenshot", "close"],
      "description": "open=打开网页；snapshot=列出当前屏内可交互元素（带 ref）；scroll=下翻一屏并重新列出；click=按 ref 点击；fill=按 ref 输入；back=后退；console=读取本会话页面的控制台输出与报错；screenshot=截取当前视口画面（改变页面状态的动作完成后会自动附最新截图，一般无需显式调用）；close=关闭本会话页面"
    },
    "url": { "type": "string", "description": "action=open 时必填：完整 URL（http:// 或 https://）" },
    "headless": { "type": "boolean", "description": "action=open 时可选，默认 true（无头）；false 弹出有头浏览器窗口（调试用）" },
    "ref": { "type": "string", "description": "action=click/fill 时必填：snapshot 输出里的 [序号]，如 \"3\"" },
    "text": { "type": "string", "description": "action=fill 时必填：要输入的内容" }
  },
  "required": ["action"]
}`)
}

type browserArgs struct {
	Action   string `json:"action"`
	URL      string `json:"url"`
	Ref      string `json:"ref"`
	Text     string `json:"text"`
	Headless *bool  `json:"headless,omitempty"` // 指针区分"没传"（默认 true）与显式 false
}

// headless 返回本次 open 的窗口形态：缺省 true（无头是默认形态，不打扰用户），
// 只有显式 false 才弹有头窗口。
func (a browserArgs) headless() bool { return a.Headless == nil || *a.Headless }

// Execute 执行一个动作。业务失败（打不开、ref 不存在、不可输入）走 IsError——
// 模型据此换路（换 URL / 重新 snapshot）；error 只用于参数坏到无法执行。
// 调用方 ctx 的取消会桥接进 chromedp：用户点停止时当前动作立刻打断（见 withTimeout）。
func (t *Tool) Execute(ctx context.Context, args json.RawMessage) (tools.ToolResult, error) {
	// 用户已点停止：立刻返回，不再启动/驱动浏览器
	if err := ctx.Err(); err != nil {
		return canceled(), nil
	}
	var a browserArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return tools.ToolResult{Content: "参数不是合法 JSON：" + err.Error(), IsError: true}, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	switch a.Action {
	case "open":
		if strings.TrimSpace(a.URL) == "" {
			return fail(ctx, "open 需要 url 参数（完整 http/https 地址）"), nil
		}
		if !strings.HasPrefix(a.URL, "http://") && !strings.HasPrefix(a.URL, "https://") {
			return fail(ctx, "url 必须以 http:// 或 https:// 开头（本工具不处理其它协议）"), nil
		}
		if err := t.ensure(a.headless()); err != nil {
			return fail(ctx, "启动浏览器失败：%v", err), nil
		}
		if err := t.run(ctx, chromedp.Navigate(a.URL), chromedp.WaitVisible("body", chromedp.ByQuery)); err != nil {
			return fail(ctx, "打开 %s 失败：%v（页面不存在、超时或拒绝连接）", a.URL, err), nil
		}
		list, err := t.snapshotHydration(ctx)
		if err != nil {
			return fail(ctx, "页面已打开但读取元素失败：%v", err), nil
		}
		return t.withShot(ctx, tools.ToolResult{
			Content: "已打开 " + t.location(ctx) + "\n\n" + list,
			Title:   "open " + shorten(a.URL), Op: "exec",
		}), nil
	case "snapshot":
		if err := t.ensureRunning(); err != nil {
			return fail(ctx, "%v", err), nil
		}
		list, err := t.snapshotLocked(ctx)
		if err != nil {
			return fail(ctx, "读取页面元素失败：%v", err), nil
		}
		return tools.ToolResult{Content: t.location(ctx) + "\n\n" + list, Title: "snapshot", Op: "exec"}, nil
	case "scroll":
		if err := t.ensureRunning(); err != nil {
			return fail(ctx, "%v", err), nil
		}
		var raw string
		if err := t.run(ctx, chromedp.EvaluateAsDevTools(scrollJS, &raw)); err != nil {
			return fail(ctx, "滚动失败：%v", err), nil
		}
		var pos struct {
			Y      int  `json:"y"`
			Bottom bool `json:"bottom"`
		}
		if err := json.Unmarshal([]byte(raw), &pos); err != nil {
			return fail(ctx, "解析滚动位置失败：%v", err), nil
		}
		list, err := t.snapshotLocked(ctx)
		if err != nil {
			return fail(ctx, "滚动后读取元素失败：%v", err), nil
		}
		head := fmt.Sprintf("已下翻一屏（y=%d", pos.Y)
		if pos.Bottom {
			head += "，已到页底"
		}
		return t.withShot(ctx, tools.ToolResult{Content: head + "）\n\n" + list, Title: "scroll", Op: "exec"}), nil
	case "click", "fill":
		idx, err := parseRef(a.Ref)
		if err != nil {
			return fail(ctx, "%s 需要 ref 参数（snapshot 输出里的 [序号]）：%v", a.Action, err), nil
		}
		if a.Action == "fill" && a.Text == "" {
			return fail(ctx, "fill 需要 text 参数（要输入的内容）"), nil
		}
		if err := t.ensureRunning(); err != nil {
			return fail(ctx, "%v", err), nil
		}
		var reply string
		js := clickFillJS(idx, a.Action == "fill", a.Text)
		if err := t.run(ctx, chromedp.EvaluateAsDevTools(js, &reply)); err != nil {
			return fail(ctx, "%s 执行失败：%v", a.Action, err), nil
		}
		switch {
		case reply == "notfound":
			return tools.ToolResult{Content: fmt.Sprintf("ref [%s] 不存在：页面可能已变化，请重新 snapshot", a.Ref), IsError: true}, nil
		case reply == "notfillable":
			return tools.ToolResult{Content: fmt.Sprintf("ref [%s] 不是可输入元素（input/textarea/select/contenteditable）", a.Ref), IsError: true}, nil
		}
		out := fmt.Sprintf("已在 [%s] 执行 %s", a.Ref, map[bool]string{true: "输入", false: "点击"}[a.Action == "fill"])
		if a.Action == "fill" {
			out += fmt.Sprintf("（%d 字）", len([]rune(a.Text)))
		}
		return t.withShot(ctx, tools.ToolResult{
			Content: out + "\n" + t.location(ctx),
			Title:   a.Action + " [" + a.Ref + "]", Op: "exec",
		}), nil
	case "back":
		if err := t.ensureRunning(); err != nil {
			return fail(ctx, "%v", err), nil
		}
		// 用导航历史 API 而不是 chromedp.NavigateBack：后者等"导航完成"事件的
		// 实现在简单页面上会挂死到超时（实测）；历史条目是确定性的。
		// cdproto 命令不经过 chromedp.Run 时必须自己挂 target executor——
		// Run 只给它自己的 Action 注入 executor，裸 .Do 会得到 invalid context。
		c := chromedp.FromContext(t.tctx)
		if c == nil || c.Target == nil {
			return fail(ctx, "浏览器连接不可用：请重新 browser open"), nil
		}
		cctx, cancel := t.withTimeout(ctx, actionTimeout)
		defer cancel()
		cctx = cdp.WithExecutor(cctx, c.Target)
		idx, hist, err := page.GetNavigationHistory().Do(cctx)
		if err != nil {
			return fail(ctx, "读取导航历史失败：%v", err), nil
		}
		if idx <= 0 || len(hist) == 0 {
			return tools.ToolResult{Content: "没有可后退的历史\n" + t.location(ctx), Title: "back", Op: "exec"}, nil
		}
		if err := page.NavigateToHistoryEntry(hist[idx-1].ID).Do(cctx); err != nil {
			return fail(ctx, "后退失败：%v", err), nil
		}
		// 后退后等 body 就绪（短限，不占主动作的超时预算）。等待失败不否定后退——
		// 历史条目已切换、位置照报；但失败必须可见（页面可能仍在加载，模型据此
		// 决定重试或再等），不能悄悄吞掉装作一切正常。
		wctx, wcancel := t.withTimeout(ctx, 5*time.Second)
		defer wcancel()
		note := ""
		if err := chromedp.Run(wctx, chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
			note = "（页面就绪等待失败，可能仍在加载）"
		}
		return t.withShot(ctx, tools.ToolResult{
			Content: "已后退" + note + "\n" + t.location(ctx), Title: "back", Op: "exec",
		}), nil
	case "console":
		if err := t.ensureRunning(); err != nil {
			return fail(ctx, "%v", err), nil
		}
		t.logsMu.Lock()
		lines := make([]string, len(t.logs))
		copy(lines, t.logs) // 拷贝后立刻放锁：回调还在持续写入，不能持锁做 Join
		t.logsMu.Unlock()
		if len(lines) == 0 {
			return tools.ToolResult{Content: "（控制台暂无输出）", Title: "console", Op: "exec"}, nil
		}
		return tools.ToolResult{Content: strings.Join(lines, "\n"), Title: "console", Op: "exec"}, nil
	case "screenshot":
		if err := t.ensureRunning(); err != nil {
			return fail(ctx, "%v", err), nil
		}
		rel, note, err := t.captureShot(ctx)
		if err != nil {
			return fail(ctx, "截图失败：%v", err), nil
		}
		content := "已截图 " + rel
		if note != "" {
			content += "\n（" + note + "）"
		}
		return tools.ToolResult{
			Content: content, Title: "screenshot", Op: "exec",
			Visual: &tools.VisualInfo{Shot: rel, URL: t.pageURL(ctx), Console: t.consoleTail()},
		}, nil
	case "close":
		t.shutdownLocked()
		return tools.ToolResult{Content: "已关闭本会话页面", Title: "close", Op: "exec"}, nil
	default:
		return tools.ToolResult{Content: fmt.Sprintf("未知 action %q（可选：open/snapshot/scroll/click/fill/back/console/screenshot/close）", a.Action), IsError: true}, nil
	}
}

// Close 收掉本会话的 tab（会话删除/应用退出时调用；幂等）。不关浏览器进程——
// 进程是共享资产，由 Pool.Close 统一收。
func (t *Tool) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.shutdownLocked()
	return nil
}

// ---- 内部 ----

// ensure 惰性建 tab（首次 open）。首个 tab 会拉起浏览器进程，限时防挂死。
// headless 指定本次 open 要的窗口形态：形态一致就复用现有 tab；不一致（无头
// 切有头调试、或反向）就地重建——旧 tab 的导航历史随之作废，属预期行为。
func (t *Tool) ensure(headless bool) error {
	if t.tctx != nil {
		if t.tctx.Err() == nil && t.headless == headless {
			return nil
		}
		// 形态切换 / 浏览器进程被重启过，旧 tab 已失效：就地重置后重建（cancel 幂等）
		t.tcancel()
		t.tctx, t.tcancel = nil, nil
		t.logsMu.Lock()
		t.logs = nil
		t.logsMu.Unlock()
	}
	bp, err := t.pool.procFor(headless)
	if err != nil {
		return err
	}
	tctx, tcancel := chromedp.NewContext(bp.allocCtx)

	// 预启动（首次 Run 创建 target；首个 tab 还会拉起进程）。两个纪律：
	//   - **不能**用限时派生 ctx 来跑首启——函数返回时派生 ctx 取消，刚建好的
	//     tab 会跟着被关掉，后续动作全部 "context canceled"（实测踩过）；
	//   - 启动必须有限时：挂死也要在 launchTimeout 内给模型一个可见的失败。
	done := make(chan error, 1)
	go func() { done <- chromedp.Run(tctx) }()
	select {
	case err := <-done:
		if err != nil {
			tcancel()
			return fmt.Errorf("启动失败：%v", err)
		}
	case <-time.After(launchTimeout):
		tcancel()
		return fmt.Errorf("启动超时（%v）：浏览器进程可能被安全软件拦截", launchTimeout)
	}

	// 控制台与页面错误进本 tab 的环形缓冲（console 动作读取；模型据此自己发现 JS 报错）
	chromedp.ListenTarget(tctx, t.handleEvent)
	t.headless = headless
	t.tctx, t.tcancel = tctx, tcancel
	return nil
}

// ensureRunning 校验本 tab 可用（调用方已持 mu，不可重入 mu）。
func (t *Tool) ensureRunning() error {
	if t.tctx == nil {
		return fmt.Errorf("浏览器尚未启动：先 browser open 打开一个页面")
	}
	if t.tctx.Err() != nil {
		return fmt.Errorf("浏览器进程已退出：请重新 browser open")
	}
	return nil
}

// withTimeout 派生 tab 限时 ctx，并把调用方取消桥接进来：用户点停止时回合 ctx
// 被取消 → 立刻 cancel 本动作的 ctx（chromedp 只认 context 取消，不桥接就穿不透，
// 动作会跑满超时上限）。桥接 goroutine 在 cctx 结束后自然退出（cancel 由 defer 保证）。
func (t *Tool) withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	cctx, cancel := context.WithTimeout(t.tctx, d)
	if ctx == nil {
		return cctx, cancel
	}
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-cctx.Done():
		}
	}()
	return cctx, cancel
}

// run 在单动作超时内执行 chromedp 动作。必须派生自**tab 上下文**（chromedp 靠
// context 值链找到 allocator/浏览器/tab；派生自调用方的裸 ctx 会得到 invalid context）。
func (t *Tool) run(ctx context.Context, actions ...chromedp.Action) error {
	if err := t.ensureRunning(); err != nil {
		return err
	}
	cctx, cancel := t.withTimeout(ctx, actionTimeout)
	defer cancel()
	return chromedp.Run(cctx, actions...)
}

// snapshotHydration open 后列元素。SPA 水合前 body 已可见但框架未挂载，元素常为
// 空——只在空结果时等一拍重试一次（正常页面不多等，水合慢的页面能自救）。
func (t *Tool) snapshotHydration(ctx context.Context) (string, error) {
	list, err := t.snapshotLocked(ctx)
	if err != nil || list != emptySnapshot {
		return list, err
	}
	timer := time.NewTimer(hydrateWait)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return t.snapshotLocked(ctx)
}

// snapshotLocked 列出**当前屏内**可见可交互元素（[ref] 标签 + 文本），带总量上限
// 控 token。判"屏内"而不是"有尺寸"：长页面必须翻屏才能看到后面的元素，与
// scroll 动作配套，超长列表才是可操作的。无文本元素（纯图标按钮）也要列出——
// 模型不知道它存在就永远点不到。
func (t *Tool) snapshotLocked(ctx context.Context) (string, error) {
	var raw string
	if err := t.run(ctx, chromedp.EvaluateAsDevTools(elementsJS(), &raw)); err != nil {
		return "", err
	}
	var items []struct {
		Index int    `json:"i"`
		Tag   string `json:"tag"`
		Text  string `json:"text"`
		Href  string `json:"href"`
		Vis   bool   `json:"vis"`
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return "", fmt.Errorf("解析元素列表失败：%v", err)
	}
	var b strings.Builder
	shown := 0
	for _, it := range items {
		if !it.Vis {
			continue
		}
		if shown >= maxElements {
			b.WriteString(fmt.Sprintf("…（还有 %d 个屏内元素未列出，可用 browser scroll 下翻后重新 snapshot）\n", len(items)-shown))
			break
		}
		text := it.Text
		if text == "" {
			text = "（无文本）"
		}
		b.WriteString(fmt.Sprintf("[%d] %s %s", it.Index, it.Tag, text))
		if it.Href != "" {
			b.WriteString(" → " + it.Href)
		}
		b.WriteString("\n")
		shown++
	}
	if shown == 0 {
		return emptySnapshot, nil
	}
	b.WriteString("\n（ref=方括号里的序号；用 browser click/fill 的 ref 参数引用）")
	return strings.TrimRight(b.String(), "\n"), nil
}

// location 读当前页 URL 与标题（辅助信息，失败静默为空——主动作已成功，不让
// 一次读址失败毁掉动作结果）。
func (t *Tool) location(ctx context.Context) string {
	if t.tctx == nil {
		return ""
	}
	cctx, cancel := t.withTimeout(ctx, 5*time.Second)
	defer cancel()
	var u, title string
	if err := chromedp.Run(cctx, chromedp.Location(&u), chromedp.Title(&title)); err != nil {
		return ""
	}
	if title != "" {
		return title + "  " + u
	}
	return u
}

// pageURL 读当前页 URL（驾驶舱数据用，只要地址不要标题；失败静默为空，同
// location 的取舍）。
func (t *Tool) pageURL(ctx context.Context) string {
	if t.tctx == nil {
		return ""
	}
	cctx, cancel := t.withTimeout(ctx, 5*time.Second)
	defer cancel()
	var u string
	if err := chromedp.Run(cctx, chromedp.Location(&u)); err != nil {
		return ""
	}
	return u
}

// consoleTail 返回控制台尾部若干条（拷贝后立刻放锁：事件回调还在持续写入，
// 不能持锁做拷贝以外的事）。只读尾部、绝不截短环形缓冲——console 动作仍看全量。
func (t *Tool) consoleTail() []string {
	t.logsMu.Lock()
	defer t.logsMu.Unlock()
	lo := 0
	if len(t.logs) > consoleTailN {
		lo = len(t.logs) - consoleTailN
	}
	out := make([]string, len(t.logs)-lo)
	copy(out, t.logs[lo:])
	return out
}

// withShot 给成功结果补上驾驶舱数据：当前视口截图 + 页面 URL + 控制台尾部。
// 截图失败不毁掉主动作——视觉是增强不是本体，失败原因进 Content 尾注（模型
// 与用户都看得到），其余数据照常携带。调用方持 t.mu（主动作路径上串行执行）。
func (t *Tool) withShot(ctx context.Context, res tools.ToolResult) tools.ToolResult {
	vi := &tools.VisualInfo{URL: t.pageURL(ctx), Console: t.consoleTail()}
	rel, note, err := t.captureShot(ctx)
	switch {
	case err != nil:
		note = fmt.Sprintf("截图失败：%v", err)
	default:
		vi.Shot = rel
	}
	res.Visual = vi
	if note != "" {
		res.Content += "\n（" + note + "）"
	}
	return res
}

// captureShot 截当前视口并落盘，返回 browser-shots 根下的相对路径（正斜杠：
// 前端拼接与 IPC 传输都用 /）与可选降级说明（压缩失败原图保存）。文件名
// shot-NNNN.ext 会话内单调递增——可预测、按序可拼、重开页面也不覆盖。
// 调用方持 t.mu。
func (t *Tool) captureShot(ctx context.Context) (rel, note string, err error) {
	if err := t.ensureRunning(); err != nil {
		return "", "", err
	}
	cctx, cancel := t.withTimeout(ctx, shotTimeout)
	defer cancel()
	var raw []byte
	if err := chromedp.Run(cctx, chromedp.CaptureScreenshot(&raw)); err != nil {
		return "", "", err
	}
	data, ext, cerr := shrinkShot(raw)
	if cerr != nil {
		// 压缩失败不挡截图：原图照存（大就大），降级原因随结果可见
		data, ext, note = raw, "png", fmt.Sprintf("截图压缩失败，原图保存：%v", cerr)
	}
	t.shotSeq++
	name := fmt.Sprintf("shot-%04d.%s", t.shotSeq, ext)
	dir := filepath.Join(t.pool.shotRoot, t.sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("创建截图目录失败：%w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		return "", "", fmt.Errorf("写入截图失败：%w", err)
	}
	pruneShots(dir, shotKeepPerSession)
	return path.Join(t.sessionID, name), note, nil
}

// pruneShots 按会话保留最近 keep 张截图，超出删最旧（captureShot 落盘后调用，
// 第二轮体检 R5）。判新旧按修改时间而非文件名：shot-NNNN 序号只在本进程内
// 单调，应用重启后归零会重名覆盖，名字序跨重启不可靠，落盘时间才是先后。
// 删除失败只记日志不报错：旧图清不掉不该毁掉刚成功的主动作，残留由下次裁剪兜底。
func pruneShots(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // 目录不存在/不可读：没有可裁的图，不算失败
	}
	type shot struct {
		name string
		mod  time.Time
	}
	var shots []shot
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // 拿不到元数据的条目不参与排序，也不挡其余裁剪
		}
		shots = append(shots, shot{name: e.Name(), mod: info.ModTime()})
	}
	if len(shots) <= keep {
		return
	}
	sort.Slice(shots, func(i, j int) bool { return shots[i].mod.Before(shots[j].mod) })
	for _, s := range shots[:len(shots)-keep] {
		if err := os.Remove(filepath.Join(dir, s.name)); err != nil {
			applog.Errorf("清理旧截图失败（%s/%s）：%v", filepath.Base(dir), s.name, err)
		}
	}
}

// shrinkShot 超限截图瘦身：先等比缩到宽 ≤ maxShotWidth，再转 JPEG——PNG 截图
// 的体积大头在照片级像素上，JPEG 一档就够。未超限原样返回（PNG 无损是默认
// 形态，不为省一点点体积交压缩税）。ext 是落盘扩展名。
func shrinkShot(raw []byte) (data []byte, ext string, err error) {
	if len(raw) <= maxShotBytes {
		return raw, "png", nil
	}
	src, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("解码截图失败：%w", err)
	}
	if w := src.Bounds().Dx(); w > maxShotWidth {
		src = scaleImage(src, maxShotWidth)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: shotQuality}); err != nil {
		return nil, "", fmt.Errorf("编码 JPEG 失败：%w", err)
	}
	return buf.Bytes(), "jpg", nil
}

// scaleImage 等比缩放（box 平均：目标像素取源上对应矩形的像素均值）。stdlib
// 没有现成缩放器（x/image 不入依赖），box 十几行够用——截图缩放是"缩略看得清"，
// 不追求重采样算法的完美。
func scaleImage(src image.Image, targetW int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	targetH := sh * targetW / sw
	if targetH < 1 {
		targetH = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	for y := 0; y < targetH; y++ {
		sy0, sy1 := y*sh/targetH, (y+1)*sh/targetH
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < targetW; x++ {
			sx0, sx1 := x*sw/targetW, (x+1)*sw/targetW
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var r, g, bl, a, n uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					c := src.At(b.Min.X+sx, b.Min.Y+sy)
					cr, cg, cb, ca := c.RGBA() // 16 位预乘，右移 8 位归一到 8 位
					r += uint64(cr >> 8)
					g += uint64(cg >> 8)
					bl += uint64(cb >> 8)
					a += uint64(ca >> 8)
					n++
				}
			}
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(r / n), G: uint8(g / n), B: uint8(bl / n), A: uint8(a / n),
			})
		}
	}
	return dst
}

// safeDirName 把会话 ID 清洗成安全的单级目录名：只保留字母数字与 -_，其余
// 一律替换为 _（分隔符与点号不允许——落盘点绝不能被异常 ID 引出截图根）。
func safeDirName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "adhoc"
	}
	return b.String()
}

// handleEvent 收本 tab 的事件（chromedp 事件 goroutine 调用）：只碰 logsMu 保护
// 的日志缓冲，不碰 mu——Execute 持 mu 阻塞在动作上时控制台仍能持续记录，两边
// 并发也不再是数据竞争。
func (t *Tool) handleEvent(ev any) {
	switch e := ev.(type) {
	case *runtime.EventConsoleAPICalled:
		parts := make([]string, 0, len(e.Args))
		for _, a := range e.Args {
			// Value 是参数的 JSON 编码原文：字符串参数要真正解码（否则中文是
			// \u 转义形式，模型和用户都读不了）；对象/数组保留 JSON 原文。
			var v any
			if err := json.Unmarshal(a.Value, &v); err == nil {
				if s, ok := v.(string); ok {
					parts = append(parts, s)
					continue
				}
			}
			if s := strings.TrimSpace(string(a.Value)); s != "" {
				parts = append(parts, s)
			}
		}
		t.appendLog("[" + string(e.Type) + "] " + strings.Join(parts, " "))
	case *runtime.EventExceptionThrown:
		if e.ExceptionDetails != nil && e.ExceptionDetails.Exception != nil {
			t.appendLog("[页面错误] " + strings.TrimSpace(e.ExceptionDetails.Exception.Description))
		}
	}
}

// appendLog 追加一条控制台日志（环形缓冲；只由事件回调调用，走 logsMu）。
func (t *Tool) appendLog(line string) {
	if line == "" {
		return
	}
	t.logsMu.Lock()
	defer t.logsMu.Unlock()
	t.logs = append(t.logs, time.Now().Format("15:04:05")+" "+line)
	if len(t.logs) > consoleRingSize {
		t.logs = t.logs[len(t.logs)-consoleRingSize:]
	}
}

// shutdownLocked 关 tab 并清日志（调用方持 mu）。
func (t *Tool) shutdownLocked() {
	if t.tcancel != nil {
		t.tcancel()
		t.tcancel = nil
	}
	t.tctx = nil
	t.logsMu.Lock()
	t.logs = nil
	t.logsMu.Unlock()
}

// canceled 是"用户点了停止"的统一结果：不是业务失败，模型无须也没机会补救。
func canceled() tools.ToolResult {
	return tools.ToolResult{Content: "动作已被用户取消", Op: "exec"}
}

// fail 是动作失败的统一出口：调用方已取消时报"已取消"，否则按业务错误（IsError）
// 给出原因——绝不把 context canceled 伪装成"页面不存在"误导模型。
func fail(ctx context.Context, format string, a ...any) tools.ToolResult {
	if ctx != nil && ctx.Err() != nil {
		return canceled()
	}
	return tools.ToolResult{Content: fmt.Sprintf(format, a...), IsError: true}
}

// ---- 纯函数助手（可单测） ----

// elementsJS 返回枚举**当前屏内**可交互元素的 JS 表达式（返回 JSON 字符串；
// 同步、无外部依赖）。snapshot 与 click/fill 必须用同一段选择器与顺序，ref 才稳定。
func elementsJS() string {
	return `(function(){
  var els = document.querySelectorAll('` + selJS + `');
  var vh = window.innerHeight || document.documentElement.clientHeight;
  var out = [];
  els.forEach(function(el, i){
    var r = el.getBoundingClientRect();
    var t = ((el.innerText || el.value || el.placeholder || el.getAttribute('aria-label') || '') + '').trim().replace(/\s+/g, ' ').slice(0, ` + itoa(maxTextRunes) + `);
    out.push({i: i, tag: el.tagName.toLowerCase(), text: t,
      href: (el.tagName === 'A' && el.href) ? String(el.href).slice(0, 80) : '',
      vis: r.width > 0 && r.height > 0 && r.bottom > 0 && r.top < vh});
  });
  return JSON.stringify(out);
})()`
}

// scrollJS 向下滚动约一屏；返回落点与是否到底（到底时 snapshot 不变，模型能看出来）。
const scrollJS = `(function(){
  window.scrollBy(0, Math.round(window.innerHeight*0.9));
  var doc = document.documentElement;
  var max = Math.max(doc.scrollHeight, document.body.scrollHeight) - window.innerHeight;
  return JSON.stringify({y: Math.round(window.scrollY), bottom: window.scrollY >= max - 2});
})()`

// clickFillJS 生成点击/输入的 JS：REF 为已校验的非负整数，TEXT 经 JSON 转义嵌入。
func clickFillJS(ref int, fill bool, text string) string {
	esc, _ := json.Marshal(text)
	target := fmt.Sprintf(`var els = document.querySelectorAll('%s'); var el = els[%d];
  if (!el) { return 'notfound'; }`, selJS, ref)
	if fill {
		return `(function(){ ` + target + `;
  el.focus();
  if (el.isContentEditable) { el.innerText = ` + string(esc) + `; el.dispatchEvent(new Event('input', {bubbles:true})); }
  else if ('value' in el) { el.value = ` + string(esc) + `; el.dispatchEvent(new Event('input', {bubbles:true})); el.dispatchEvent(new Event('change', {bubbles:true})); }
  else { return 'notfillable'; }
  return 'ok';
})()`
	}
	return `(function(){ ` + target + `;
  el.scrollIntoView({block:'center'});
  el.click();
  return 'ok';
})()`
}

// parseRef 校验 ref：必须是**完整**的非负十进制整数（要作数字下标注入 JS——
// 严格解析是注入面的最后防线，"1;2" 这类一律拒绝）。
func parseRef(s string) (int, error) {
	s = strings.Trim(strings.TrimSpace(s), "[]")
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || strconv.Itoa(n) != s {
		return 0, fmt.Errorf("ref %q 不是合法序号", s)
	}
	return n, nil
}

// FindExecPath 按常见安装位置找 Edge/Chrome。TIANCODE_BROWSER 环境变量**优先且严格**：
// 设了就直接用（哪怕文件不存在——启动时会报出这个路径，配置错误必须可见，不静默回落）。
// 都没有则返回空串（调用方显式报错，不猜）。
func FindExecPath() string {
	if p := os.Getenv("TIANCODE_BROWSER"); p != "" {
		return p
	}
	prog := os.Getenv("ProgramFiles")
	progX86 := os.Getenv("ProgramFiles(x86)")
	local := os.Getenv("LocalAppData")
	candidates := []string{
		filepath.Join(progX86, `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(prog, `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(local, `Microsoft\Edge\Application\msedge.exe`),
		filepath.Join(prog, `Google\Chrome\Application\chrome.exe`),
		filepath.Join(progX86, `Google\Chrome\Application\chrome.exe`),
		filepath.Join(local, `Google\Chrome\Application\chrome.exe`),
	}
	for _, p := range candidates {
		if p != "" && fileExists(p) {
			return p
		}
	}
	return ""
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func shorten(s string) string {
	if len(s) <= 60 {
		return s
	}
	return s[:57] + "..."
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
