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

// 审批闸门真链路验证（默认跳过）：模型要跑命令时必须被拦住，
// 拒绝后必须能改道完成整轮，且**不真执行任何命令**。
//
// 为什么必须真链路验证：审批是"问-等-答"的时序交互，单测只能覆盖两端的配对，
// 无法证明"流式进行中真正挂起并在答复后恢复"。
func TestLiveApprovalGate(t *testing.T) {
	base := os.Getenv("TIANCODE_LIVE_BASEURL")
	key := os.Getenv("TIANCODE_LIVE_APIKEY")
	model := os.Getenv("TIANCODE_LIVE_MODEL")
	if base == "" || key == "" || model == "" {
		t.Skip("未设置 TIANCODE_LIVE_* 环境变量，跳过审批闸门验证")
	}

	work := t.TempDir()
	s, err := NewChatService(Config{
		BaseURL: base, APIKey: key, Model: model,
		WorkDir: work, DataDir: t.TempDir(),
		ChannelsPath: filepath.Join(t.TempDir(), "channels.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	// 开启审批：只拦 shell（ADR-0007 最小清单）
	events := make(chan ApprovalEvent, 2)
	s.SetApprovalHandler(func(e ApprovalEvent) { events <- e })
	if err := s.SetApprovalPolicy([]string{"shell"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	ch, err := s.Send(ctx, "s-apr", "必须使用 shell 工具执行命令 echo hello（不要凭记忆直接回答），然后复述命令输出。")
	if err != nil {
		t.Fatalf("Send 失败：%v", err)
	}

	// 并发消费流：否则审批事件与"流提前结束"只能二选一地等待，失败时看不到真原因
	type outcome struct {
		text string
		end  llm.EndReason
		err  error
	}
	streamDone := make(chan outcome, 1)
	go func() {
		var sb strings.Builder
		var end llm.EndReason
		var endErr error
		for c := range ch {
			sb.WriteString(c.Delta)
			if c.EndReason != llm.EndNone {
				end, endErr = c.EndReason, c.Err
			}
		}
		streamDone <- outcome{sb.String(), end, endErr}
	}()

	// 竞速：审批事件 vs 流终态 vs 超时（诊断信息足够定位是"没调工具"还是"审批没拦"）
	var ev ApprovalEvent
	select {
	case ev = <-events:
	case o := <-streamDone:
		t.Fatalf("流在收到审批事件前结束（模型可能未调用工具或审批未生效）：end=%d err=%v text=%q",
			o.end, o.err, o.text)
	case <-time.After(90 * time.Second):
		t.Fatal("超时：既无审批事件也无终态")
	}
	if ev.ID == "" || ev.ToolName != "shell" {
		t.Fatalf("审批事件不符（应拦下 shell）：%+v", ev)
	}
	if !strings.Contains(ev.Arguments, "command") {
		t.Fatalf("审批参数应原样透传命令字段：%q", ev.Arguments)
	}
	t.Logf("已拦截：tool=%s args=%s", ev.ToolName, ev.Arguments)

	// 拒绝 → 模型应收到"用户拒绝执行"并改道完成整轮
	if err := s.ResolveApproval(ev.ID, false, "验证：自动拒绝"); err != nil {
		t.Fatalf("提交拒绝失败：%v", err)
	}

	var got outcome
	select {
	case got = <-streamDone:
	case <-time.After(90 * time.Second):
		t.Fatal("拒绝后流未收束（审批答复没有唤醒挂起的执行）")
	}
	if got.end != llm.EndDone {
		t.Fatalf("拒绝后应正常收束为 EndDone，实际 %d (err=%v) text=%q", got.end, got.err, got.text)
	}
	if strings.TrimSpace(got.text) == "" {
		t.Fatal("拒绝后模型应有可见回复（改道说明）")
	}
	t.Logf("拒绝后模型改道回复：%q", strings.TrimSpace(got.text))

	// 未决请求必须清空（不泄漏）
	s.mu.Lock()
	pending := len(s.pendingApprovals)
	s.mu.Unlock()
	if pending != 0 {
		t.Fatalf("审批结束后仍有未决请求：%d", pending)
	}
}

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
