package app

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// 限速：窗口内的第二条静默丢弃（nil 返回，不报错——连发保护不是故障）。
func TestSendNotification_RateLimited(t *testing.T) {
	var calls int
	old := notifyRunner
	notifyRunner = func(string, []string) error { calls++; return nil }
	defer func() { notifyRunner = old }()

	notifyMu.Lock()
	notifyLastSent = time.Now() // 刚发过一条
	notifyMu.Unlock()

	if err := (&Bind{}).SendNotification("标题", "正文"); err != nil {
		t.Fatalf("限速窗口内应静默丢弃而非报错：%v", err)
	}
	if calls != 0 {
		t.Fatalf("限速窗口内不应启动进程，实际 %d", calls)
	}
}

// 正常路径：参数进脚本、限速时间被推进。
func TestSendNotification_Runs(t *testing.T) {
	var gotCmd []string
	old := notifyRunner
	notifyRunner = func(_ string, args []string) error { gotCmd = args; return nil }
	defer func() { notifyRunner = old }()
	notifyMu.Lock()
	notifyLastSent = time.Time{} // 重置限速
	notifyMu.Unlock()

	if err := (&Bind{}).SendNotification("tiancode 完成", "会话 s1 的回合已结束"); err != nil {
		t.Fatalf("通知不应报错：%v", err)
	}
	if len(gotCmd) == 0 {
		t.Fatal("应启动 powershell")
	}
	script := gotCmd[len(gotCmd)-1]
	if !strings.Contains(script, "tiancode 完成") || !strings.Contains(script, "会话 s1 的回合已结束") {
		t.Fatalf("标题与正文应进脚本：%q", script)
	}
}

// 执行失败上抛（尽力而为，但绝不静默）。
func TestSendNotification_ErrorPropagates(t *testing.T) {
	old := notifyRunner
	notifyRunner = func(string, []string) error { return errors.New("boom") }
	defer func() { notifyRunner = old }()
	notifyMu.Lock()
	notifyLastSent = time.Time{}
	notifyMu.Unlock()

	if err := (&Bind{}).SendNotification("t", "b"); err == nil {
		t.Fatal("执行失败应上抛")
	}
}

// 文本边界：超长截断、XML 特殊字符转义、空标题+空正文拒绝。
func TestSendNotification_TextBoundaries(t *testing.T) {
	notifyMu.Lock()
	notifyLastSent = time.Time{}
	notifyMu.Unlock()
	if err := (&Bind{}).SendNotification("  ", "  "); err == nil {
		t.Fatal("全空白应拒绝")
	}

	notifyMu.Lock()
	notifyLastSent = time.Time{}
	notifyMu.Unlock()
	var script string
	old := notifyRunner
	notifyRunner = func(_ string, args []string) error { script = args[len(args)-1]; return nil }
	defer func() { notifyRunner = old }()
	if err := (&Bind{}).SendNotification("标题<script>&\"'", strings.Repeat("正", notifyBodyLimit+50)); err != nil {
		t.Fatalf("通知不应报错：%v", err)
	}
	s := script
	if !strings.Contains(s, "标题&lt;script&gt;&amp;&#34;&#39;") {
		t.Fatalf("XML 特殊字符应转义：%q", s[len(s)-200:])
	}
	if strings.Count(s, "正") > notifyBodyLimit/3+3 { // 每字 3 字节，截断后不会全量出现
		t.Fatalf("正文应被截断：%d", strings.Count(s, "正"))
	}
}
