package app

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"tiancode/internal/core/llm"
)

// 结构锁（0.0.26）：谁都不许再把 ChatService.Send* 返回的通道丢掉。
// 丢掉 = 没人消费 = 整条流式链路在第一块后死锁（见本例文件头的实机故障说明）。
func TestSendPathsNeverDiscardStream(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	// 丢掉返回值的写法：`_, err := b.chat.Send...` / `_, sendErr := ...`
	discard := regexp.MustCompile(`_\s*,\s*\w+\s*:=\s*b\.chat\.Send`)
	if m := discard.FindString(code); m != "" {
		t.Fatalf("发送路径不得丢弃流通道（没人消费会让内核死锁）：%s", m)
	}
	// 两条发送路径都必须走同一个消费函数
	if n := strings.Count(code, "drainTurn(ctx, sessionID, ch,"); n < 2 {
		t.Fatalf("Send 与 SendWithAttachments 必须共用 drainTurn 消费流，实际出现 %d 次", n)
	}
}

// 0.0.26 回归锁：**两条**发送路径都必须消费流。
//
// 实机故障"上传文件或图片后发出去没有回复"的根因：SendWithAttachments 把
// `chat.SendWithAttachments` 返回的通道直接丢掉（`_`）——没有人消费 →
// 服务转发循环卡在 `out <- c` → 内核卡在第二块 → 账本只留一条增量、
// 界面永远"正在思考"、90 秒后被看门狗当成"上游黑洞"收掉（日志里连上游超时都没有）。
// 请求侧一直是好的（裸 HTTP 与自家 provider 都能拿回完整回答）。
func TestDrainTurnConsumesEveryChunkKind(t *testing.T) {
	ch := make(chan llm.StreamChunk, 8)
	ch <- llm.StreamChunk{Delta: "你"}
	ch <- llm.StreamChunk{Thinking: "想一下"}
	ch <- llm.StreamChunk{Usage: &llm.Usage{PromptTokens: 11, CompletionTokens: 22, TotalTokens: 33}}
	ch <- llm.StreamChunk{ToolEvent: &llm.ToolEvent{Name: "browser", Status: "success", CallID: "c1", Title: "open",
		Shot: "s-1/shot-0001.png", PageURL: "http://127.0.0.1:5173/", Console: []string{"12:00:01 [log] ready"}}}
	ch <- llm.StreamChunk{Todo: &llm.TodoEvent{Items: []llm.TodoItem{{Text: "干活", Status: "pending"}}}}
	ch <- llm.StreamChunk{Context: &llm.ContextEvent{EstimatedTokens: 10, BudgetTokens: 20}}
	ch <- llm.StreamChunk{EndReason: llm.EndDone}
	close(ch)

	var got []string
	var payloads []any
	drainTurn(context.Background(), "s1", ch, func(name string, payload any) {
		got = append(got, name)
		payloads = append(payloads, payload)
	}, nil)

	want := []string{"chat:chunk", "chat:chunk", "chat:usage", "chat:tool", "chat:todo", "chat:context", "chat:terminal"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("事件序列不对：%v", got)
	}
	// 终态带 sessionID（前端按会话归位，多会话并行时不能串）
	term, _ := payloads[6].(map[string]any)
	if term["sessionID"] != "s1" || term["endReason"] != int(llm.EndDone) {
		t.Fatalf("终态载荷不对：%v", term)
	}
	// 驾驶舱数据（浏览器工具）：截图路径/URL/控制台尾部必须随 chat:tool 流到前端
	tool, _ := payloads[3].(map[string]any)
	if tool["shot"] != "s-1/shot-0001.png" || tool["url"] != "http://127.0.0.1:5173/" {
		t.Fatalf("chat:tool 缺驾驶舱字段：%v", tool)
	}
	console, ok := tool["console"].([]string)
	if !ok || len(console) != 1 || !strings.Contains(console[0], "ready") {
		t.Fatalf("chat:tool 的 console 必须是尾部原样数组：%v", tool["console"])
	}
}

// 无终态即关闭（极端时序：取消恰逢发送受阻）→ 必须兜底合成终态，
// 前端靠它解锁输入框（C-APP-2 的 UI 侧保证）。
func TestDrainTurnSynthesizesTerminalOnSilentClose(t *testing.T) {
	ch := make(chan llm.StreamChunk, 1)
	ch <- llm.StreamChunk{Delta: "半截"}
	close(ch)

	var last string
	var lastPayload map[string]any
	drainTurn(context.Background(), "s2", ch, func(name string, payload any) {
		last = name
		if m, ok := payload.(map[string]any); ok {
			lastPayload = m
		}
	}, nil)
	if last != "chat:terminal" {
		t.Fatalf("必须合成终态，最后一个是 %q", last)
	}
	if lastPayload["endReason"] != int(llm.EndCancelled) {
		t.Fatalf("合成终态应为 cancelled：%v", lastPayload)
	}
}

// 消费到流关闭即返回（不提前退出）：这就是"没人消费 = 内核死锁"的反面。
// 用一个"发满缓冲就阻塞"的通道模拟上游：只有消费方在读，写入方才走得下去。
func TestDrainTurnDrainsUntilClose(t *testing.T) {
	ch := make(chan llm.StreamChunk) // 无缓冲：没人读就写不进去
	done := make(chan int, 1)
	go func() {
		n := 0
		for i := 0; i < 50; i++ {
			ch <- llm.StreamChunk{Delta: "字"}
			n++
		}
		ch <- llm.StreamChunk{EndReason: llm.EndDone}
		close(ch)
		done <- n
	}()
	var chunks int
	drainTurn(context.Background(), "s3", ch, func(name string, _ any) {
		if name == "chat:chunk" {
			chunks++
		}
	}, nil)
	if n := <-done; n != 50 || chunks != 50 {
		t.Fatalf("必须一路消费到关闭：写入 %d 块、收到 %d 块", n, chunks)
	}
}
