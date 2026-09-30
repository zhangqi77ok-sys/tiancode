package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/platform/applog"
)

// 0.0.09 用户要求："内部搞个日志，快速定位问题"。本测试断言一轮对话在日志里
// 留下完整的诊断轨迹：轮次开始（含文本/附件数）→ 上游请求（host/模型/字节数）
// → 建流完成 → 轮次结束（终态/耗时）。"卡在哪一层"看最后一行即可定位。
func TestE2E_AppLogRecordsTurnLifecycle(t *testing.T) {
	logDir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"你好\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)

	s := newChannelService(t, Config{})
	if _, err := s.AddChannel(testChannel("log-ch", srv.URL)); err != nil {
		t.Fatal(err)
	}
	// 构造后切换日志目录：断言在测试自己的目录里（NewChatService 已挂 DataDir/logs）
	applog.SetDir(logDir)
	t.Cleanup(func() { applog.SetDir("") })

	ch, err := s.Send(context.Background(), "s-log", "日志冒烟问题")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}

	files, _ := filepath.Glob(filepath.Join(logDir, "app-*.log"))
	if len(files) == 0 {
		t.Fatal("没有生成日志文件")
	}
	data, _ := os.ReadFile(files[0])
	log := string(data)
	for _, want := range []string{
		"turn start session=s-log", // 轮次开始（会话/文本摘要/附件数）
		"text=\"日志冒烟问题\"",          // 文本摘要可见（排障要能对上用户描述）
		"turn model=",              // 模型名
		"upstream request host=",   // 上游请求发出（有这行后仍无 connected = 挂在建流）
		"upstream connected",       // 建流完成（流层看门狗开始计时）
		"turn end session=s-log",   // 轮次结束
		"reason=done",              // 终态原因（EndReason.String 保证可读）
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("日志缺少 %q：\n%s", want, log)
		}
	}
}
