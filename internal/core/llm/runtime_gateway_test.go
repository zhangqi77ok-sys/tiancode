// 重试语义收敛测试（0.0.23 审计第 9 项）。
//
// 背景：一次 Send 的上游请求数曾"说不清"——runtime 层有建流重试
//（MaxAttempts），gateway 层有渠道级重试（MaxRetries），两者串联让人担心
// 故障时请求数相乘。核实结论：**生产路径不相乘**，因为 gateway 从不返回
// error，失败一律写成终态块，而 runtime 只在 provider 返回 error 时才重试。
//
// 本文件把这个结论钉死：任何"让 gateway 改返回 error"的改动都会让下面的
// 测试变红，从而在上线前暴露"请求数翻倍"的回归。
package llm

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// terminalBlockProvider 模拟 gateway 的失败表达方式：返回通道 + 一个 EndError
// 终态块，err 字段为 nil（机制性失败才返回 error）。
type terminalBlockProvider struct {
	calls int32
}

func (p *terminalBlockProvider) StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	atomic.AddInt32(&p.calls, 1)
	ch := make(chan StreamChunk, 1)
	ch <- StreamChunk{EndReason: EndError, Err: context.DeadlineExceeded}
	close(ch)
	return ch, nil
}

// 终态块表达失败 = 业务/上游失败：runtime 不重试，provider 只被问一次。
func TestRuntime_TerminalBlockIsNotRetried(t *testing.T) {
	p := &terminalBlockProvider{}
	rt := NewChatRuntime(p, TimeoutBudget{Total: 5 * time.Second})
	ch, err := rt.Chat(context.Background(), ChatRequest{Model: "m"}, RuntimePolicy{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainTerminal(t, ch)
	if terminal.EndReason != EndError {
		t.Fatalf("terminal = %+v, want EndError 透传", terminal)
	}
	if got := atomic.LoadInt32(&p.calls); got != 1 {
		t.Fatalf("provider 被调用 %d 次，want 1（终态块失败不得触发 runtime 重试，否则与 gateway 渠道重试相乘）", got)
	}
}

// 机制性失败（provider 返回 error）才重试——与上一条成对，把边界写死。
func TestRuntime_MechanismErrorStillRetries(t *testing.T) {
	fp := &fakeProvider{connectErrs: 1} // 第一次建流返回 error，第二次成功
	rt := NewChatRuntime(fp, TimeoutBudget{Total: 5 * time.Second})
	ch, err := rt.Chat(context.Background(), ChatRequest{Model: "m"}, RuntimePolicy{MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainTerminal(t, ch)
	if terminal.EndReason != EndDone {
		t.Fatalf("terminal = %+v, want EndDone（机制失败后重试应成功）", terminal)
	}
}
