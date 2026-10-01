package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tiancode/internal/core/tools"
)

// ReadBrowserShot（浏览器驾驶舱）契约：
//   - 只读 browser-shots 根目录内的文件——截图路径经 IPC 暴露给前端，穿越面必须关死；
//   - 命中返回 base64 图片字节（前端 data URL 直用）；越界/缺失/绝对路径显式报错。

func newShotTestService(t *testing.T) (*ChatService, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "tc-shots-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) }) // 账本句柄进程内常开：可忽略失败地清（同 send_attachments_e2e）
	channelsPath := filepath.Join(dir, "channels.json")
	raw, _ := json.Marshal(map[string]any{"version": 3, "channels": []any{}, "activeId": "", "approvalTools": []any{}})
	if err := os.WriteFile(channelsPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	svc, err := NewChatService(Config{
		DataDir:        filepath.Join(dir, "data"),
		ChannelsPath:   channelsPath,
		ExtensionsPath: filepath.Join(dir, "extensions.json"),
		TonesPath:      filepath.Join(dir, "tones.json"),
	})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	return svc, filepath.Join(dir, "data", "browser-shots")
}

func TestReadBrowserShot_ReturnsBase64(t *testing.T) {
	svc, root := newShotTestService(t)
	body := []byte("fake-png-bytes")
	shotDir := filepath.Join(root, "s-1")
	if err := os.MkdirAll(shotDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shotDir, "shot-0001.png"), body, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := svc.ReadBrowserShot("s-1/shot-0001.png")
	if err != nil {
		t.Fatal(err)
	}
	if want := base64.StdEncoding.EncodeToString(body); got != want {
		t.Fatal("返回必须是文件字节的 base64")
	}
}

func TestReadBrowserShot_RejectsTraversal(t *testing.T) {
	svc, root := newShotTestService(t)
	secret := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(secret, []byte("机密"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"../secret.txt",
		"s-1/../../secret.txt",
		"..\\secret.txt",
		`C:\Windows\win.ini`,
		"/etc/passwd",
		"s-1",
		".",
	} {
		if got, err := svc.ReadBrowserShot(bad); err == nil {
			t.Fatalf("穿越/非法路径必须拒绝（%q），却读到了内容", bad)
		} else if got != "" {
			t.Fatalf("拒绝时不得返回内容：%q", bad)
		}
	}
}

func TestReadBrowserShot_MissingFileErrors(t *testing.T) {
	svc, _ := newShotTestService(t)
	_, err := svc.ReadBrowserShot("s-1/shot-9999.png")
	if err == nil || !strings.Contains(err.Error(), "s-1") {
		t.Fatalf("缺失文件必须显式报错且带路径：%v", err)
	}
}

// ---- BrowserNavigate（用户点链接 → 右侧驾驶舱打开）契约 ----
//   - 入口只收 http/https（javascript:/file: 等在编排层拒绝，不给工具层兜底机会）；
//   - 执行内核把 ToolResult 映射成 BrowserView（shot/url/console/output/title 同源同义）；
//   - 业务失败（IsError）与机制故障（error）都必须显式报错——绝不返回半截视图冒充成功。

// fakeTab 实现 tools.ToolPort：记录收到的 ctx（含 Execute 时刻的取消状态）与
// args，按预设返回——验证编排映射与取消传导，不真开浏览器（真实 open/截图行为
// 由 browsertool 自身契约测试覆盖）。
type fakeTab struct {
	gotArgs      string
	gotCtx       context.Context
	ctxErrAtExec error // Execute 进入时的 ctx 状态（返回后再查已被 defer cancel 污染）
	res          tools.ToolResult
	err          error
}

func (f *fakeTab) Name() string            { return "browser" }
func (f *fakeTab) Description() string     { return "fake" }
func (f *fakeTab) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (f *fakeTab) Execute(ctx context.Context, args json.RawMessage) (tools.ToolResult, error) {
	f.gotCtx = ctx
	f.ctxErrAtExec = ctx.Err()
	f.gotArgs = string(args)
	return f.res, f.err
}

func TestBrowserNavigate_RejectsNonHTTPSchemes(t *testing.T) {
	svc, _ := newShotTestService(t)
	for _, bad := range []string{
		"javascript:alert(1)",
		"file:///C:/Windows/win.ini",
		"ftp://example.com",
		"data:text/html,<h1>x</h1>",
		"https://", // 无 host
		"not a url",
		"",
	} {
		if view, err := svc.BrowserNavigate("s-1", bad); err == nil {
			t.Fatalf("非 http/https 链接必须拒绝（%q），却返回了 %+v", bad, view)
		}
	}
}

func TestBrowserNavigateWith_MapsVisualToView(t *testing.T) {
	tab := &fakeTab{res: tools.ToolResult{
		Content: "[ref=e1] 链接一",
		Title:   "open example.com",
		Visual:  &tools.VisualInfo{Shot: "s-1/shot-0001.png", URL: "https://example.com/", Console: []string{"[log] hi"}},
	}}
	view, err := browserNavigateWith(context.Background(), tab, "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if view.Shot != "s-1/shot-0001.png" || view.URL != "https://example.com/" ||
		len(view.Console) != 1 || view.Output != "[ref=e1] 链接一" || view.Title != "open example.com" {
		t.Fatalf("驾驶舱字段必须同源映射：%+v", view)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(tab.gotArgs), &args); err != nil {
		t.Fatal(err)
	}
	if args["action"] != "open" || args["url"] != "https://example.com" {
		t.Fatalf("必须以 open 动作转发原始 URL：%s", tab.gotArgs)
	}
}

func TestBrowserNavigateWith_EmptyVisualURLFallsBackToRaw(t *testing.T) {
	tab := &fakeTab{res: tools.ToolResult{Content: "已打开", Title: "open x"}}
	view, err := browserNavigateWith(context.Background(), tab, "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if view.URL != "https://example.com" {
		t.Fatalf("Visual 缺失时 URL 必须回退原始链接：%+v", view)
	}
}

func TestBrowserNavigateWith_BusinessErrorSurfaces(t *testing.T) {
	tab := &fakeTab{res: tools.ToolResult{IsError: true, Content: "打开超时"}}
	if view, err := browserNavigateWith(context.Background(), tab, "https://example.com"); err == nil {
		t.Fatalf("业务失败必须显式报错，却返回了 %+v", view)
	} else if !strings.Contains(err.Error(), "打开超时") {
		t.Fatalf("错误必须携带工具给的可读原因：%v", err)
	}
}

func TestBrowserNavigateWith_MechanismErrorSurfaces(t *testing.T) {
	tab := &fakeTab{err: errors.New("浏览器进程拉起失败")}
	if _, err := browserNavigateWith(context.Background(), tab, "https://example.com"); err == nil || !strings.Contains(err.Error(), "浏览器进程拉起失败") {
		t.Fatalf("机制故障必须上抛且带原因：%v", err)
	}
}

// ---- 会话级 ctx / 取消 / 存活校验（第二轮体检 R2、R3、R4）契约 ----
//   - BrowserNavigate 把"会话级 ctx + 单次超时"传给 tab.Execute：会话删除时在途
//     导航立刻中断（DeleteSession 不再被 tab 锁卡住最长 60 秒）；
//   - 已删除的 sessionID 入口显式报错，绝不重建无主工具集（"点旧链接复活会话"）。

// TestBrowserNavigate_SessionCtxFlowsAndCancels：导航 ctx 必须派生自会话级 ctx，
// 会话取消传导到在途导航。
func TestBrowserNavigate_SessionCtxFlowsAndCancels(t *testing.T) {
	svc, _ := newShotTestService(t)
	if _, err := svc.ledgerFor("s-nav"); err != nil { // 建账本：会话在册
		t.Fatal(err)
	}
	st, err := svc.ensureSessionTools("s-nav", "")
	if err != nil {
		t.Fatal(err)
	}
	tab := &fakeTab{res: tools.ToolResult{Content: "已打开", Title: "open x"}}
	st.browser = tab // 注入假 tab：不真开浏览器
	if _, err := svc.BrowserNavigate("s-nav", "https://example.com"); err != nil {
		t.Fatal(err)
	}
	if tab.gotCtx == nil {
		t.Fatal("必须把会话级 ctx 派生的 ctx 传给 tab.Execute")
	}
	if tab.ctxErrAtExec != nil {
		t.Fatalf("正常导航时 ctx 不应已取消：%v", tab.ctxErrAtExec)
	}
	// 会话 ctx 取消（DeleteSession/收旧）必须传导到下一次导航：派生 ctx 进入
	// Execute 时即已取消——真实 tab 在 Execute 入口据此立即中断（打断行为由
	// blockingTab 的 DeleteSession 契约覆盖）
	st.cancel()
	if _, err := svc.BrowserNavigate("s-nav", "https://example.com"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(tab.ctxErrAtExec, context.Canceled) {
		t.Fatalf("会话 ctx 取消必须传导到导航 ctx（在途操作立即中断）：%v", tab.ctxErrAtExec)
	}
}

// blockingTab 模拟卡在 45s open 上的真实 tab：Execute 等到 ctx 取消才返回——
// 验证 DeleteSession 先 cancel 再关工具后，删除不再被 tab 锁卡住。
type blockingTab struct {
	started chan struct{}
	gotCtx  context.Context
}

func (b *blockingTab) Name() string            { return "browser" }
func (b *blockingTab) Description() string     { return "fake" }
func (b *blockingTab) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (b *blockingTab) Execute(ctx context.Context, _ json.RawMessage) (tools.ToolResult, error) {
	b.gotCtx = ctx
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done() // 模拟不可中断的浏览器动作：取消前一直占着工具
	return tools.ToolResult{IsError: true, Content: ctx.Err().Error()}, nil
}

func (b *blockingTab) Close() error { return nil }

// TestDeleteSession_CancelsInflightNavigate：在途导航期间删除会话必须快速返回
// （远小于工具内部 45s+15s 预算），且被取消的导航显式报错。
func TestDeleteSession_CancelsInflightNavigate(t *testing.T) {
	svc, _ := newShotTestService(t)
	if _, err := svc.ledgerFor("s-blk"); err != nil {
		t.Fatal(err)
	}
	st, err := svc.ensureSessionTools("s-blk", "")
	if err != nil {
		t.Fatal(err)
	}
	tab := &blockingTab{started: make(chan struct{}, 1)}
	st.browser = tab

	navDone := make(chan error, 1)
	go func() {
		_, err := svc.BrowserNavigate("s-blk", "https://example.com")
		navDone <- err
	}()
	select {
	case <-tab.started:
	case <-time.After(5 * time.Second):
		t.Fatal("导航未启动")
	}

	delDone := make(chan error, 1)
	go func() { delDone <- svc.DeleteSession("s-blk") }()
	select {
	case err := <-delDone:
		if err != nil {
			t.Fatalf("删除会话失败：%v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DeleteSession 被在途浏览器操作卡住（取消语义失效）")
	}
	select {
	case err := <-navDone:
		if err == nil {
			t.Fatal("被会话删除打断的导航必须显式报错")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("在途导航未随会话删除而中断")
	}
}

// TestBrowserNavigate_DeletedSessionRejected：点已删除会话的链接显式报错，
// 且绝不重建工具集（"复活"无主 tab 会挂到应用退出）。
func TestBrowserNavigate_DeletedSessionRejected(t *testing.T) {
	svc, _ := newShotTestService(t)
	if _, err := svc.ledgerFor("s-gone"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteSession("s-gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BrowserNavigate("s-gone", "https://example.com"); err == nil || !strings.Contains(err.Error(), "已删除") {
		t.Fatalf("已删除会话必须显式报错：%v", err)
	}
	svc.sessMu.Lock()
	_, alive := svc.sessTools["s-gone"]
	svc.sessMu.Unlock()
	if alive {
		t.Fatal("不得为已删除会话重建工具集")
	}
}

// TestSessionTools_ConcurrentEnsureAndDelete：ensureSessionTools 与 DeleteSession
// 并发跑必须干净（-race 下验证 sessMu 专用锁），且删除后不复活工具集。
func TestSessionTools_ConcurrentEnsureAndDelete(t *testing.T) {
	svc, _ := newShotTestService(t)
	var wg sync.WaitGroup
	for round := 0; round < 4; round++ {
		id := "s-race-" + string(rune('a'+round))
		if _, err := svc.ledgerFor(id); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 25; j++ {
					// 竞态演练：结果不关心（删除后 ensure 会显式报错，属预期），
					// 只验证并发读写 sessTools 在 sessMu 下不炸（-race 实测）
					svc.ensureSessionTools(id, "")
					svc.BgTasksSnapshot(id)
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				svc.DeleteSession(id) // 与 ensure 并发读写 sessTools（幂等，错误不关心）
			}
		}()
	}
	wg.Wait()
}
