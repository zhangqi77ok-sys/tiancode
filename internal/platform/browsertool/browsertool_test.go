package browsertool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tiancode/internal/core/tools"
)

// 测试分两层：
//   - 纯函数（schema/JS 生成/ref 校验/浏览器发现）不依赖浏览器，始终跑；
//   - 端到端（真无头浏览器 + 本地 httptest 页面）在找不到 Edge/Chrome 的机器上跳过，
//     本机（Windows + Edge）必跑——这是"模型改完前端自己开页面看效果"的主路径。

func TestSchema_ActionEnum(t *testing.T) {
	var s struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(NewPool().NewTab("s-1").Schema(), &s); err != nil {
		t.Fatal(err)
	}
	want := "open,snapshot,scroll,click,fill,back,console,screenshot,close"
	if got := strings.Join(s.Properties.Action.Enum, ","); got != want {
		t.Fatalf("action enum = %q, want %q", got, want)
	}
}

// open 的 headless 参数（驾驶舱）：缺省必须按 true（无头）处理——JSON 里没传
// 与显式 true 同语义，只有显式 false 才弹有头窗口。
func TestSchema_OpenHeadlessParam(t *testing.T) {
	var s struct {
		Properties struct {
			Headless struct {
				Type string `json:"type"`
			} `json:"headless"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(NewPool().NewTab("s-1").Schema(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Properties.Headless.Type != "boolean" {
		t.Fatalf("open.headless 应是 boolean 参数，got %q", s.Properties.Headless.Type)
	}
	var a browserArgs
	if err := json.Unmarshal([]byte(`{"action":"open","url":"http://x/"}`), &a); err != nil {
		t.Fatal(err)
	}
	if !a.headless() {
		t.Fatal("headless 缺省必须为 true（无头是默认形态）")
	}
	if err := json.Unmarshal([]byte(`{"action":"open","headless":false}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.headless() {
		t.Fatal("显式 false 必须生效（调试要弹有头窗口）")
	}
}

// 会话 ID 直接用作截图子目录名（外部输入经前端传来）：非法字符必须就地替换，
// 路径分隔符与点号不允许——落盘点绝不能被引出 browser-shots 根。
func TestSafeDirName(t *testing.T) {
	for in, want := range map[string]string{
		"s-1":         "s-1",
		"s_att.9":     "s_att_9",
		"../evil":     "___evil",
		"a/b\\c:d":    "a_b_c_d",
		"":            "adhoc",
		"正常-会话-ID-01": "__-__-ID-01",
	} {
		if got := safeDirName(in); got != want {
			t.Fatalf("safeDirName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRef(t *testing.T) {
	for in, want := range map[string]int{"3": 3, " 12 ": 12, "[7]": 7} {
		got, err := parseRef(in)
		if err != nil || got != want {
			t.Fatalf("parseRef(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "-1", "1;2"} {
		if _, err := parseRef(bad); err == nil {
			t.Fatalf("parseRef(%q) 应当拒绝（要注入 JS，非法值是注入面）", bad)
		}
	}
}

// click/fill 的 JS 里 ref 是数字插值、text 是 JSON 转义——生成的代码必须语法完整且不含裸引号漏洞。
func TestClickFillJS_EscapesText(t *testing.T) {
	js := clickFillJS(3, true, "他说\"hi\"\n第二行</script>")
	// json.Marshal 的转义产物：引号 → \"（反斜杠+引号）、换行 → \n（反斜杠+n 两个字符）、
	// < > & → \u003c \u003e \u0026（HTML 转义——恰好挡住 </script> 逃逸）
	for _, want := range []string{`他说\"hi\"`, `\n第二行`, `\u003c/script\u003e`} {
		if !strings.Contains(js, want) {
			t.Fatalf("text 转义缺 %s：%s", want, js)
		}
	}
	if !strings.Contains(js, "els[3]") || strings.Contains(js, "els[3;") {
		t.Fatalf("ref 必须是纯数字下标：%s", js)
	}
	if !strings.Contains(js, "dispatchEvent(new Event('input'") {
		t.Fatalf("输入必须派发 input 事件（React/Vue 受控组件才认）：%s", js)
	}
	if js := clickFillJS(2, false, ""); !strings.Contains(js, "el.click()") {
		t.Fatalf("click 应调用原生 click：%s", js)
	}
}

// elementsJS 的"可见"必须是屏内判定（rect 与 viewport 相交）：长页面翻屏（scroll）
// 才能看到后面的元素，这是 scroll 动作存在的前提。
func TestElementsJS_VisibleMeansInViewport(t *testing.T) {
	js := elementsJS()
	for _, want := range []string{"r.bottom > 0", "r.top < vh", "innerHeight"} {
		if !strings.Contains(js, want) {
			t.Fatalf("可见性判定缺屏内条件 %s：%s", want, js)
		}
	}
}

// 环境变量**严格优先**：设了就直接用（哪怕路径不存在）——启动失败会把这个路径
// 原样报给用户（配置错误必须可见），绝不静默回落到自动探测。
func TestFindExecPath_EnvWins(t *testing.T) {
	t.Setenv("TIANCODE_BROWSER", "C:\\nonexistent\\browser.exe")
	if got := FindExecPath(); got != "C:\\nonexistent\\browser.exe" {
		t.Fatalf("环境变量必须严格优先，got %q", got)
	}
	t.Setenv("TIANCODE_BROWSER", "")
	if hasLocalBrowser() {
		if got := FindExecPath(); !strings.Contains(strings.ToLower(got), "msedge") && !strings.Contains(strings.ToLower(got), "chrome") {
			t.Fatalf("自动探测应找到 Edge/Chrome，got %q", got)
		}
	}
}

func hasLocalBrowser() bool { return FindExecPath() != "" }

// 并行会话各自首次 browser open 会并发进入 procFor（Tool.ensure 只持本 tab 的
// mu，从不持 p.mu）：p.procs 必须由 procFor 自持锁读写——否则 -race 下是数据
// 竞争，最坏 "concurrent map writes" 直接崩；双进程竞写 last-wins 还会泄漏一个
// 浏览器进程与临时目录（Pool.Close 永远收不到）。收敛断言防的正是后者。
// TIANCODE_BROWSER 指向不存在的文件即可：procFor 只建 allocator 上下文，
// 真拉起进程发生在 ensure 的首次 Run。
func TestPoolProcFor_ConcurrentFirstOpen(t *testing.T) {
	t.Setenv("TIANCODE_BROWSER", "C:\\nonexistent\\browser.exe")
	p := NewPoolAt(t.TempDir())
	defer p.Close()
	const n = 8
	bps := make([]*browserProc, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bps[i], errs[i] = p.procFor(true)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("并发 procFor[%d] 失败：%v", i, err)
		}
		if bps[i] != bps[0] {
			t.Fatalf("并发首次 open 必须收敛到同一进程：第 %d 个调用拿到了不同的 browserProc（last-wins 会泄漏进程与临时目录）", i)
		}
	}
}

// shrinkShot 契约：未超限原样返回（PNG 无损是默认形态）；超限等比缩到宽 ≤1280
// 并转 JPEG——两者都只对"大图"发生，小截图不为省体积交压缩税。
func TestShrinkShot(t *testing.T) {
	small := makeNoisePNG(t, 200, 120)
	data, ext, err := shrinkShot(small)
	if err != nil || ext != "png" || string(data) != string(small) {
		t.Fatalf("小图必须原样返回：ext=%s err=%v len=%d/%d", ext, err, len(data), len(small))
	}

	big := makeNoisePNG(t, 1600, 1200) // 噪声 PNG 压不动，必然超 1.5MB
	if len(big) <= maxShotBytes {
		t.Fatalf("测试前置失效：噪声图应超过上限（实际 %d bytes）", len(big))
	}
	data, ext, err = shrinkShot(big)
	if err != nil {
		t.Fatal(err)
	}
	if ext != "jpg" {
		t.Fatalf("超限图应转 JPEG，got %s", ext)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("产物必须是合法 JPEG：%v", err)
	}
	if w := img.Bounds().Dx(); w > maxShotWidth {
		t.Fatalf("缩后宽度必须 ≤%d，got %d", maxShotWidth, w)
	}
}

// scaleImage 等比缩放：宽对齐目标，高按比例；不整除也要算对。
func TestScaleImage_Dimensions(t *testing.T) {
	for _, c := range []struct{ sw, sh, tw, eh int }{
		{1600, 1200, 1280, 960},
		{100, 50, 50, 25},
		{1299, 3, 1280, 2}, // 高按整数比例 floor（极端比例由 ≥1 兜底）
	} {
		got := scaleImage(image.NewRGBA(image.Rect(0, 0, c.sw, c.sh)), c.tw)
		if got.Bounds().Dx() != c.tw || got.Bounds().Dy() != c.eh {
			t.Fatalf("scaleImage(%dx%d → w%d) = %v", c.sw, c.sh, c.tw, got.Bounds())
		}
	}
}

// makeNoisePNG 生成 w×h 的伪随机噪声 PNG（噪声压不动，体积可预期地大）。
func makeNoisePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(rng.Intn(256)), G: uint8(rng.Intn(256)),
				B: uint8(rng.Intn(256)), A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// 端到端：真无头浏览器访问本地页面——snapshot/click/fill/console/back/scroll
// 全链路，外加"用户点停止必须打断动作"。
func TestBrowser_EndToEnd(t *testing.T) {
	exe := FindExecPath()
	if exe == "" {
		t.Skip("本机没有 Edge/Chrome，跳过真浏览器用例")
	}
	clicks := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		clicks++
		fmt.Fprintf(w, `<!doctype html><html><head><title>测试页 %d</title></head><body>
			<h1>hello</h1>
			<button id="go">点我计数</button>
			<input id="q" placeholder="搜索">
			<a href="/next">下一页</a>
			<button id="icon" style="width:30px;height:20px;"></button>
			<script>
			document.getElementById('go').addEventListener('click', function(){
				console.log('被点击了', %d);
				var h = document.createElement('h2'); h.textContent = '已点击';
				document.body.appendChild(h);
			});
			</script></body></html>`, clicks, clicks)
	}))
	defer srv.Close()
	// 长页：60 个高按钮——首屏列不全，scroll 翻屏应能一路看到最后一个
	long := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><body style="margin:0">`)
		for i := 0; i < 60; i++ {
			fmt.Fprintf(w, `<button style="display:block;width:120px;height:100px;">长页按钮%d</button>`, i)
		}
		fmt.Fprint(w, `</body></html>`)
	}))
	defer long.Close()

	pool := NewPoolAt(t.TempDir()) // 截图落盘到临时目录，不污染真实用户数据目录
	defer pool.Close()
	tl := pool.NewTab("s-e2e")
	ctx := context.Background()
	call := func(args string) tools.ToolResult {
		t.Helper()
		res, err := tl.Execute(ctx, json.RawMessage(args))
		if err != nil {
			t.Fatalf("Execute(%s) 机制错误：%v", args, err)
		}
		if res.IsError {
			t.Fatalf("Execute(%s) 业务失败：%s", args, res.Content)
		}
		return res
	}

	// open：标题 + 元素清单（含无文本的图标按钮——列不出来模型就永远点不到）
	open := call(fmt.Sprintf(`{"action":"open","url":%q}`, srv.URL+"/page1"))
	if !strings.Contains(open.Content, "测试页 1") || !strings.Contains(open.Content, "点我计数") {
		t.Fatalf("open 结果缺标题/元素：%s", open.Content)
	}
	if !strings.Contains(open.Content, "（无文本）") {
		t.Fatalf("snapshot 必须列出无文本元素（图标按钮）：%s", open.Content)
	}
	// 驾驶舱：open 自动附截图 + URL；文件必须真实落在会话子目录（路径可预测）
	if open.Visual == nil || open.Visual.Shot == "" || open.Visual.URL != srv.URL+"/page1" {
		t.Fatalf("open 必须附带驾驶舱数据：%+v", open.Visual)
	}
	shotBody, err := os.ReadFile(filepath.Join(pool.shotRoot, filepath.FromSlash(open.Visual.Shot)))
	if err != nil || len(shotBody) == 0 {
		t.Fatalf("截图必须真实落盘（%s）：%v", open.Visual.Shot, err)
	}
	if _, err := png.Decode(bytes.NewReader(shotBody)); err != nil {
		t.Fatalf("自动截图必须是合法 PNG：%v", err)
	}
	if !strings.HasPrefix(open.Visual.Shot, "s-e2e/shot-") {
		t.Fatalf("截图相对路径必须是 会话/文件名 形态：%s", open.Visual.Shot)
	}

	// fill：往输入框（snapshot 枚举顺序：[0]=button、[1]=input、[2]=link、[3]=空按钮）
	fillRes := call(`{"action":"fill","ref":"1","text":"关键词"}`)
	if fillRes.Visual == nil || fillRes.Visual.Shot == "" || fillRes.Visual.Shot == open.Visual.Shot {
		t.Fatalf("状态改变动作必须各附一张新截图：%+v", fillRes.Visual)
	}

	// click：触发页面 JS（console.log + DOM 变化）
	clickRes := call(`{"action":"click","ref":"0"}`)
	if clickRes.Visual == nil || len(clickRes.Visual.Console) == 0 || !strings.Contains(strings.Join(clickRes.Visual.Console, "\n"), "被点击了") {
		t.Fatalf("click 结果必须带控制台尾部（含页面 log）：%+v", clickRes.Visual)
	}

	// console：能看到刚才那条 log（事件监听已生效）
	con := call(`{"action":"console"}`)
	if !strings.Contains(con.Content, "被点击了") {
		t.Fatalf("console 应包含页面 log：%s", con.Content)
	}

	// back：回到上一页（URL 变化可见）
	call(`{"action":"open","url":` + quote(srv.URL+"/page2") + `}`)
	call(`{"action":"back"}`)

	// scroll：长页翻屏，最终看到末尾元素（屏内判定 + 翻屏闭环）
	call(`{"action":"open","url":` + quote(long.URL) + `}`)
	var got string
	for i := 0; i < 15; i++ {
		got = call(`{"action":"scroll"}`).Content
		if strings.Contains(got, "长页按钮59") {
			break
		}
	}
	if !strings.Contains(got, "长页按钮59") {
		t.Fatalf("scroll 翻屏后应能看到末尾元素：%s", got)
	}

	// 用户点停止（动作进行中取消）：必须打断等待中的动作，而不是跑满 45s
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done(): // 浏览器取消导航断开连接，handler 立刻退出（不拖住测试）
		}
		fmt.Fprint(w, "<html><body>slow</body></html>")
	}))
	defer slow.Close()
	runCtx, runCancel := context.WithCancel(context.Background())
	type execRes struct {
		res tools.ToolResult
		err error
	}
	done := make(chan execRes, 1)
	go func() {
		res, err := tl.Execute(runCtx, json.RawMessage(fmt.Sprintf(`{"action":"open","url":%q}`, slow.URL)))
		done <- execRes{res, err}
	}()
	time.Sleep(500 * time.Millisecond) // 等动作进入页面等待
	runCancel()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("被取消的动作不应有机制错误：%v", r.err)
		}
		if r.res.IsError || !strings.Contains(r.res.Content, "取消") {
			t.Fatalf("进行中的动作应被取消打断：%+v", r.res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消必须立刻打断动作，5s 内未返回")
	}

	// 已取消的 ctx 再调用：入口直接快速返回
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if res, err := tl.Execute(cancelCtx, json.RawMessage(`{"action":"snapshot"}`)); err != nil || !strings.Contains(res.Content, "取消") {
		t.Fatalf("已取消 ctx 应立即返回取消结果：err=%v res=%+v", err, res)
	}

	// close 后再操作：显式报"先 open"，不是机制 panic
	tl.Close()
	res, err := tl.Execute(ctx, json.RawMessage(`{"action":"snapshot"}`))
	if err != nil || !res.IsError || !strings.Contains(res.Content, "先 browser open") {
		t.Fatalf("close 后应得到可见的业务错误：err=%v res=%+v", err, res)
	}

	// screenshot：显式要一张图——结果与落盘文件对得上
	tl2 := pool.NewTab("s-e2e-2")
	call2 := func(args string) tools.ToolResult {
		t.Helper()
		r, e := tl2.Execute(ctx, json.RawMessage(args))
		if e != nil || r.IsError {
			t.Fatalf("Execute(%s) 失败：err=%v res=%+v", args, e, r)
		}
		return r
	}
	call2(fmt.Sprintf(`{"action":"open","url":%q}`, srv.URL+"/page1"))
	shot := call2(`{"action":"screenshot"}`)
	if shot.Visual == nil || !strings.Contains(shot.Content, shot.Visual.Shot) {
		t.Fatalf("screenshot 结果必须带截图路径：%+v", shot)
	}
	if _, err := os.Stat(filepath.Join(pool.shotRoot, filepath.FromSlash(shot.Visual.Shot))); err != nil {
		t.Fatalf("显式截图必须落盘：%v", err)
	}

	// headless=false：同一 Pool 拉起有头进程（调试形态），open 正常工作。
	// 有头窗口在本机会闪现一瞬——这正是被测行为本身。
	headed := call2(fmt.Sprintf(`{"action":"open","url":%q,"headless":false}`, srv.URL+"/page1"))
	if headed.Visual == nil || headed.Visual.Shot == "" {
		t.Fatalf("有头 open 同样要带驾驶舱数据：%+v", headed)
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// 路由宣告契约：描述必须明说"本环境唯一的浏览器"并禁止绕道 mcp / shell start——
// 实证（2026-10-01）：缺了这两句，模型按训练习惯找 MCP 浏览器碰壁后，会误判
// "内置浏览器用不了"，让用户的"给我看看页面"落空。
func TestDescription_ClaimsExclusiveBrowser(t *testing.T) {
	d := (&Tool{}).Description()
	for _, want := range []string{"唯一的浏览器", "不要用 shell start", "不要经 mcp 找浏览器", "open(url)"} {
		if !strings.Contains(d, want) {
			t.Fatalf("描述缺少路由宣告 %q", want)
		}
	}
}

// ---- 截图裁剪（第二轮体检 R5）契约：按会话保留最近 N 张，超出删最旧 ----

// TestPruneShots_KeepsNewest：构造 60 张（修改时间递增），裁剪后只剩最新 50 张，
// 且被删的恰是最旧的 10 张。
func TestPruneShots_KeepsNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-2 * time.Hour)
	for i := 0; i < 60; i++ {
		name := filepath.Join(dir, fmt.Sprintf("shot-%04d.png", i))
		if err := os.WriteFile(name, []byte{0}, 0o600); err != nil {
			t.Fatal(err)
		}
		// 修改时间随序号递增：文件名序在跨重启（序号归零）后不可靠，裁剪按时间判新旧
		stamp := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	pruneShots(dir, shotKeepPerSession)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != shotKeepPerSession {
		t.Fatalf("裁剪后应剩 %d 张，实际 %d 张", shotKeepPerSession, len(entries))
	}
	// 最旧的 shot-0000..0009 应被删；边界上 shot-0010 与最新的 shot-0059 应保留
	if _, err := os.Stat(filepath.Join(dir, "shot-0009.png")); !os.IsNotExist(err) {
		t.Fatal("最旧的截图应被删除")
	}
	for _, keep := range []string{"shot-0010.png", "shot-0059.png"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Fatalf("应保留较新的截图 %s：%v", keep, err)
		}
	}
}

// TestPruneShots_UnderKeepNoop：不足 N 张时一张不删（也不报错）。
func TestPruneShots_UnderKeepNoop(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"shot-0001.png", "shot-0002.jpg", "note.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte{0}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pruneShots(dir, shotKeepPerSession)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("不足上限时不得删文件：剩 %d 项", len(entries))
	}
}

// TestPruneShots_MissingDirNoop：目录不存在（会话还没落过截图）不 panic、不报错。
func TestPruneShots_MissingDirNoop(t *testing.T) {
	pruneShots(filepath.Join(t.TempDir(), "no-such-dir"), shotKeepPerSession)
}
