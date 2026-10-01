// Package webfetch 实现网页正文读取工具（webfetch / 动作 fetch）。
//
// 做什么：GET 一个 http/https 地址，返回文档标题与剥除脚本/样式/导航后的正文
// 文本。模型此前没有任何读网页的能力（browser.snapshot 只给可交互元素、每个
// 截 60 字符），查文档、查报错方案是硬伤——本工具补上这条腿。本地 dev server
// 也在服务范围（不拦 localhost），前端改完页面模型能自己读。
// 被谁依赖：internal/app（装配为共享工具：不依赖工作区根，纯对话也可用）。
// 依赖谁：core/tools 端口、netproxy（出网走全局代理，gateway 同源）、
// golang.org/x/text（GBK 解码，vendor 已有）；HTML 处理刻意用 stdlib 手写——
// 只做"剥噪声取正文"，为它引一个 HTML 解析器得不偿失（见 readable.go）。
//
// 有界纪律（对齐 searchtool/fstool）：默认超时 30s、响应体上限 2MB、输出 64KB
// 头尾保留；仅 GET；HTTP 非 2xx 按业务失败表达并附错误页正文（模型可见、可推理）。
package webfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tiancode/internal/core/tools"
	"tiancode/internal/platform/netproxy"
)

const (
	defaultTimeout = 30 * time.Second
	// maxBodyBytes 是单次响应体读取上限：正文提取在内存里做，必须给硬顶。
	maxBodyBytes = 2 << 20
	// maxOutputBytes 是进模型上下文的正文预算（头尾保留，对齐其他工具的 64KiB）。
	maxOutputBytes = 64 * 1024
	// userAgent 表明身份：Go 默认 UA 会被部分站点 CDN 拒绝，通用可读标识更稳。
	userAgent = "tiancode-webfetch"
)

// Tool 是网页正文读取工具（共享工具：不依赖工作区根，无状态）。
type Tool struct {
	proxy   func() string // 全局上游代理（空 = 直连）；每次执行实时取，改配置即生效
	timeout time.Duration
}

// New 构造工具（默认 30s 超时）。proxy 是全局代理配置来源（与 gateway 同源，
// gateway.go 的 Pool.Proxy()），可为 nil = 永远直连。
func New(proxy func() string) *Tool { return NewWithTimeout(proxy, defaultTimeout) }

// NewWithTimeout 供测试注入短超时。
func NewWithTimeout(proxy func() string, d time.Duration) *Tool {
	if d <= 0 {
		d = defaultTimeout
	}
	return &Tool{proxy: proxy, timeout: d}
}

// Name 实现工具端口。
func (t *Tool) Name() string { return "webfetch" }

// Description 实现工具端口。
func (t *Tool) Description() string {
	return "读取网页正文（GET 一个 http/https 地址）：返回文档标题与剥除脚本/样式/导航后的正文文本。" +
		"查报错方案、查库文档、读本地 dev server 页面用它；它只拿文本，不能下载文件。" +
		"需要登录或点击交互的页面改用 browser；非 http/https 地址（如 file://）会被拒绝。"
}

// Schema 实现工具端口。
func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "url": {"type": "string", "description": "要读取的地址，仅支持 http/https（含本地 http://127.0.0.1:端口）"},
    "max_bytes": {"type": "integer", "description": "响应体读取上限（字节），默认 2097152（2MB）"}
  },
  "required": ["url"]
}`)
}

// Preface 是系统说明里对 webfetch 的能力边界说明（app 拼装每步系统说明时引用，
// 与 exttools.Preface 同一先例：工具的能力边界由最清楚自己的包自己写）。
func Preface() string {
	return "## 内置网页读取\n" +
		"- webfetch（动作 fetch）读取网页正文：查报错方案、查库文档、读本地 dev server 页面。" +
		"参数：url（必填，仅 http/https）、max_bytes（可选，响应体读取上限字节，默认 2MB）。\n" +
		"- 返回文档标题与剥除脚本/样式/导航后的正文文本（超 64KB 头尾保留）；" +
		"HTTP 非 2xx 时返回状态码与错误页正文。\n" +
		"- 边界：只发 GET、不执行页面 JS、不带登录态；需要登录或交互验证的页面改用 browser。"
}

// Execute 实现工具端口。fetch：GET url，按 max_bytes（默认 2MB）读响应体，
// 解码为文本（HTML 提取标题与正文），64KB 头尾保留进模型上下文。
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (res tools.ToolResult, err error) {
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{Content: "cancelled", IsError: true, TimedOut: true}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	var a struct {
		URL      string `json:"url"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return tools.ToolResult{Content: fmt.Sprintf("invalid arguments: %v", err), IsError: true}, nil
	}
	// 卡片语义标签：URL 作主标签（defer 覆盖全部返回路径，含超时/拒绝）
	defer func() {
		if res.Title == "" {
			res.Op = "fetch"
			res.Title = tools.Headline(a.URL, 80)
		}
	}()
	target, err := url.Parse(strings.TrimSpace(a.URL))
	if err != nil {
		return bizErrf("invalid url: %v", err), nil
	}
	switch target.Scheme {
	case "http", "https":
	default:
		return bizErrf("only http/https supported, got %q", target.Scheme), nil
	}
	if target.Host == "" {
		return bizErrf("url missing host: %s", a.URL), nil
	}
	capBytes := a.MaxBytes
	if capBytes <= 0 || capBytes > maxBodyBytes {
		capBytes = maxBodyBytes
	}

	client, err := netproxy.Client(nil, t.currentProxy())
	if err != nil {
		// 无效代理必须显式报错（netproxy 的纪律）：静默直连会造成"配了代理仍被封锁"类悬案
		return bizErrf("%v", err), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return bizErrf("build request: %v", err), nil
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return t.netFail(ctx, err), nil
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(capBytes)+1))
	if readErr != nil {
		// 中途断流/超时：已读到的部分按部分输出语义带出去，不静默丢弃（C-TOOL-2）
		if ctx.Err() != nil {
			out := t.present(resp, body, 0)
			out.Content += "\nTIMEOUT"
			out.TimedOut = true
			return out, nil
		}
		return bizErrf("read response: %v", readErr), nil
	}
	truncatedAt := 0
	if len(body) > capBytes {
		body = body[:capBytes]
		truncatedAt = capBytes
	}
	out := t.present(resp, body, truncatedAt)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 非 2xx 按业务失败表达，但错误页正文照给：报错原因常就在里面
		out.Content = fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, out.Content)
		out.IsError = true
	}
	return out, nil
}

// present 把响应体变成模型可见文本：解码（字符集策略）→ HTML 提取标题与正文 →
// 头尾保留；truncatedAt>0 时附截断标注（响应体被上限切过，提取结果可能不完整）。
func (t *Tool) present(resp *http.Response, body []byte, truncatedAt int) tools.ToolResult {
	contentType := resp.Header.Get("Content-Type")
	text := decodeBody(contentType, body)
	if isHTML(contentType, text) {
		title, article := extractReadable(text)
		if title != "" {
			text = title + "\n\n" + article
		} else {
			text = article
		}
	}
	if strings.TrimSpace(text) == "" {
		text = "(no readable text)"
	}
	content := tools.HeadTail(text, maxOutputBytes)
	if truncatedAt > 0 {
		content += fmt.Sprintf("\n(response truncated at %d bytes)", truncatedAt)
	}
	return tools.ToolResult{Content: content}
}

// netFail 网络失败分流：超时/取消走 TIMEOUT 语义（C-TOOL-2），其余为业务失败。
func (t *Tool) netFail(ctx context.Context, err error) tools.ToolResult {
	if ctx.Err() != nil {
		return tools.ToolResult{Content: "TIMEOUT", IsError: true, TimedOut: true}
	}
	return bizErrf("request failed: %v", err)
}

// currentProxy 取当次执行的全局代理配置（nil 源 = 直连）。
func (t *Tool) currentProxy() string {
	if t.proxy == nil {
		return ""
	}
	return t.proxy()
}

// bizErrf 构造模型可见的业务失败。
func bizErrf(format string, a ...any) tools.ToolResult {
	return tools.ToolResult{Content: fmt.Sprintf(format, a...), IsError: true}
}
