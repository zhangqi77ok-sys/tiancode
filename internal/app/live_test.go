package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/llm"
)

// 真链路端到端验证（默认跳过，仅当设置 TIANCODE_LIVE_* 时运行）：
// 跑的是**完整用例路径** —— ChatService → ChatRuntime → ProviderPort → agent 循环
// → 工具注册表 → 会话账本，全部真实组件，只把"上游"换成真网关。
//
// 为什么必须有这一层：单元测试各层都绿，仍可能因为"真实网关的报文差异"
// （工具调用分片形态、reasoning_content、非标准 finish_reason）整体跑不通。
//
//	$env:TIANCODE_LIVE_BASEURL='https://ss2a.top/v1'
//	$env:TIANCODE_LIVE_APIKEY='sk-...'
//	$env:TIANCODE_LIVE_MODEL='grok-4.7'
//	go test ./internal/app/ -run Live -v
func TestLiveAgent_FullLoop(t *testing.T) {
	base := os.Getenv("TIANCODE_LIVE_BASEURL")
	key := os.Getenv("TIANCODE_LIVE_APIKEY")
	model := os.Getenv("TIANCODE_LIVE_MODEL")
	if base == "" || key == "" || model == "" {
		t.Skip("未设置 TIANCODE_LIVE_* 环境变量，跳过真链路验证")
	}

	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "hello.txt"), []byte("第一行\n第二行\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	s, err := NewChatService(Config{
		BaseURL: base, APIKey: key, Model: model,
		WorkDir: work, DataDir: dataDir,
		ChannelsPath: filepath.Join(t.TempDir(), "channels.json"),
	})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// 指令刻意要求用工具读文件：同时验证 function calling 与工具结果回填
	ch, err := s.Send(ctx, "s-live", "用 fs 工具读取 hello.txt，然后只回复文件里第二行的内容。")
	if err != nil {
		t.Fatalf("Send 失败：%v", err)
	}

	var text strings.Builder
	toolCalls := 0
	var end llm.EndReason
	var endErr error
	for c := range ch {
		text.WriteString(c.Delta)
		if c.ToolEvent != nil {
			toolCalls++
			t.Logf("工具事件：name=%s status=%s summary=%.60s diff=%d bytes",
				c.ToolEvent.Name, c.ToolEvent.Status, c.ToolEvent.Summary, len(c.ToolEvent.Diff))
		}
		if c.EndReason != llm.EndNone {
			end, endErr = c.EndReason, c.Err
		}
	}

	if end != llm.EndDone {
		t.Fatalf("终态 = %d (err=%v)，期望 EndDone(%d)", end, endErr, llm.EndDone)
	}
	if strings.TrimSpace(text.String()) == "" {
		t.Fatal("流已结束但无任何文本")
	}
	t.Logf("真链路通过：工具调用 %d 次，回复=%q", toolCalls, strings.TrimSpace(text.String()))

	// 账本必须留下可重放的痕迹（会话恢复的基础）
	msgs, err := s.Replay("s-live")
	if err != nil {
		t.Fatalf("Replay 失败：%v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("账本为空：会话恢复将无内容可重放")
	}
	t.Logf("账本重放 %d 条消息（角色示例：%s）", len(msgs), msgs[len(msgs)-1].Role)
}
