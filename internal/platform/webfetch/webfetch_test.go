package webfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func args(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// demoPage 是正文提取契约的样例页：标题 + 正文 + 四类噪声各带一个毒丸 token，
// 断言"该留的都在、该剥的都不见"。
const demoPage = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Go 报错速查</title>
<style>.poisonStyle { color: red }</style>
<script>var poisonScript = "PoisonScriptToken";</script>
</head>
<body>
<nav><a href="/menu">PoisonNavToken</a></nav>
<header>PoisonHeaderToken</header>
<h1>安装与入门</h1>
<p>第一步 &amp; 第二步：安装依赖。</p>
<div>遇到 <code>go mod tidy</code> 报错时，先检查网络代理。</div>
<footer>PoisonFooterToken</footer>
</body>
</html>`

func TestWebFetch_ExtractsTitleAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, demoPage)
	}))
	defer srv.Close()

	tool := New(nil)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("正常页面不该失败：%q", res.Content)
	}
	for _, want := range []string{
		"Go 报错速查",         // 文档标题
		"安装与入门",           // 正文标题
		"第一步 & 第二步：安装依赖。", // 实体解码（&amp;）
		"go mod tidy",     // 行内标签剥离后文本连贯
		"先检查网络代理",
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("正文缺 %q：%q", want, res.Content)
		}
	}
	// 行内标签本身与标签噪声不进正文
	if strings.Contains(res.Content, "<code>") || strings.Contains(res.Content, "<h1>") {
		t.Fatalf("标签残留在正文里：%q", res.Content)
	}
}

func TestWebFetch_StripsHTMLNoise(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, demoPage)
	}))
	defer srv.Close()

	tool := New(nil)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	for _, poison := range []string{"PoisonScriptToken", "poisonStyle", "PoisonNavToken", "PoisonHeaderToken", "PoisonFooterToken"} {
		if strings.Contains(res.Content, poison) {
			t.Fatalf("噪声 %q 泄漏进正文：%q", poison, res.Content)
		}
	}
}

func TestWebFetch_RejectsNonHTTPScheme(t *testing.T) {
	tool := New(nil)
	for _, u := range []string{"file:///C:/Windows/win.ini", "ftp://example.com/x", "/etc/passwd"} {
		res, err := tool.Execute(context.Background(), args(t, map[string]any{"url": u}))
		if err != nil {
			t.Fatalf("%s: 机制错误 %v（业务拒绝应走 IsError）", u, err)
		}
		if !res.IsError {
			t.Fatalf("%s 必须被拒绝", u)
		}
		if !strings.Contains(res.Content, "http") {
			t.Fatalf("拒绝说明必须写明仅支持 http/https：%q", res.Content)
		}
	}
}

func TestWebFetch_DefaultBodyCapTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, strings.Repeat("A", maxBodyBytes+1))
	}))
	defer srv.Close()

	tool := New(nil)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, fmt.Sprintf("(response truncated at %d bytes)", maxBodyBytes)) {
		t.Fatalf("超默认 2MB 必须带截断标注：%q", res.Content[:200])
	}
	if len(res.Content) > 80*1024 {
		t.Fatalf("输出必须头尾保留在 64KB 量级，got %d bytes", len(res.Content))
	}
}

func TestWebFetch_MaxBytesParamTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "HeadToken"+strings.Repeat("B", 5000)+"TailToken")
	}))
	defer srv.Close()

	tool := New(nil)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"url": srv.URL, "max_bytes": 100}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "(response truncated at 100 bytes)") {
		t.Fatalf("max_bytes 生效必须带截断标注：%q", res.Content)
	}
	if !strings.Contains(res.Content, "HeadToken") {
		t.Fatalf("头部内容必须保留：%q", res.Content)
	}
	if strings.Contains(res.Content, "TailToken") {
		t.Fatalf("超上限的尾部不得进入输出：%q", res.Content)
	}
}

func TestWebFetch_TimeoutBeforeResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // 客户端超时断开即返回，测试不必等满时长
	}))
	defer srv.Close()

	tool := NewWithTimeout(nil, 100*time.Millisecond)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || !res.IsError || !strings.Contains(res.Content, "TIMEOUT") {
		t.Fatalf("无响应超时必须 TIMEOUT+IsError+TimedOut，got %+v content=%q", res, res.Content)
	}
}

func TestWebFetch_TimeoutKeepsPartialBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "PartialKeepToken")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	tool := NewWithTimeout(nil, 150*time.Millisecond)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("读一半超时必须置 TimedOut：%+v", res)
	}
	if res.IsError {
		t.Fatalf("已捕获部分输出时不该按纯失败处理：%q", res.Content)
	}
	if !strings.Contains(res.Content, "PartialKeepToken") || !strings.Contains(res.Content, "TIMEOUT") {
		t.Fatalf("部分输出 + TIMEOUT 标注必须都在：%q", res.Content)
	}
}

func TestWebFetch_GBKDeclaredCharset(t *testing.T) {
	want := "这是需要读到的中文正文"
	body, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(want))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 带引号 + 大写：Content-Type 解析必须大小写不敏感、容忍引号
		w.Header().Set("Content-Type", `text/html; charset="GBK"`)
		w.Write(body)
	}))
	defer srv.Close()

	tool := New(nil)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, want) {
		t.Fatalf("声明 GBK 必须按 GBK 解码：%q", res.Content)
	}
}

func TestWebFetch_GBKFallbackWithoutCharset(t *testing.T) {
	want := "没有声明字符集的中文正文"
	body, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(want))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html") // 无 charset：UTF-8 解不开按 GBK（对齐 shelltool）
		w.Write(body)
	}))
	defer srv.Close()

	tool := New(nil)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, want) {
		t.Fatalf("无声明且非 UTF-8 必须按 GBK 兜底解码：%q", res.Content)
	}
}

func TestWebFetch_PlainTextPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "<b>not html</b> real text")
	}))
	defer srv.Close()

	tool := New(nil)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	// 非 HTML：原样输出，不做标签剥离
	if !strings.Contains(res.Content, "<b>not html</b>") || !strings.Contains(res.Content, "real text") {
		t.Fatalf("text/plain 必须原样透传：%q", res.Content)
	}
}

func TestWebFetch_Non2xxReportsStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "resource missing")
	}))
	defer srv.Close()

	tool := New(nil)
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("404 必须按业务失败表达（模型可见）")
	}
	if !strings.Contains(res.Content, "404") || !strings.Contains(res.Content, "resource missing") {
		t.Fatalf("状态码与错误页正文都要给模型：%q", res.Content)
	}
}

func TestWebFetch_UsesGlobalProxy(t *testing.T) {
	// 假代理：目标域名不可解析，只有把请求交给这个代理才拿得到回应——
	// 代理没接上就必然失败，测试因此能锁住"出网走全局代理"这条接线。
	served := false
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		if r.Host != "proxy-target.invalid" {
			t.Errorf("代理收到的目标错误：%q", r.Host)
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ViaProxyToken")
	}))
	defer proxySrv.Close()

	tool := New(func() string { return proxySrv.URL })
	res, err := tool.Execute(context.Background(), args(t, map[string]any{"url": "http://proxy-target.invalid/hello"}))
	if err != nil {
		t.Fatal(err)
	}
	if !served {
		t.Fatal("请求没有经过代理")
	}
	if !strings.Contains(res.Content, "ViaProxyToken") {
		t.Fatalf("经代理的回应没到模型手上：%q", res.Content)
	}
}

func TestIsHTML_ContentTypeFirstSniffFallback(t *testing.T) {
	if !isHTML("text/html; charset=utf-8", "anything") {
		t.Fatal("声明 html 必须按 HTML 处理")
	}
	if !isHTML("application/xhtml+xml", "anything") {
		t.Fatal("xhtml 也是 HTML")
	}
	if isHTML("application/json", "<html>") {
		t.Fatal("json 不是 HTML")
	}
	if isHTML("text/plain", "<html>") {
		t.Fatal("text/plain 不做正文提取")
	}
	if !isHTML("", "<!DOCTYPE html><html>") {
		t.Fatal("类型缺失时按内容特征兜底识别 HTML")
	}
	if isHTML("", "plain text with < angle bracket") {
		t.Fatal("类型缺失时纯文本不得误判为 HTML")
	}
}

func TestExtractReadable_EdgeCases(t *testing.T) {
	doc := `<!DOCTYPE html><title>T &amp; T</title><!-- 注释 PoisonComment --><div>A&#20013;&#25991;B</div>` +
		`<script>var x = "</div>";</script><p>未闭合标签后的正文`
	title, text := extractReadable(doc)
	if title != "T & T" {
		t.Fatalf("标题实体解码失败：%q", title)
	}
	if strings.Contains(text, "PoisonComment") {
		t.Fatalf("注释必须整段剥离：%q", text)
	}
	if !strings.Contains(text, "A中文B") {
		t.Fatalf("数字实体必须解码：%q", text)
	}
	// script 内容里的 "</div>" 不得干扰跳转：脚本整段消失，其后的正文仍在
	if strings.Contains(text, "var x") {
		t.Fatalf("script 必须整段剥离：%q", text)
	}
	if !strings.Contains(text, "未闭合标签后的正文") {
		t.Fatalf("未闭合标签后的正文丢失：%q", text)
	}
	if !strings.Contains(text, "\n") {
		t.Fatalf("块级标签必须产生换行边界：%q", text)
	}
}
