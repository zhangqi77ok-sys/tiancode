// 0.3 折叠优先级补充测试：重复读到的同一文件只留最近一次全文；
// 对话正文是最后一级——只在更早的层级都不够时才折叠，且最近一轮永远保留。
package agent

import (
	"strings"
	"testing"

	"tiancode/internal/core/session"
)

// appendReadTurn 落一轮"读同一文件"的只读工具调用。
func appendReadTurn(t *testing.T, l *session.Ledger, id, path, content string) {
	t.Helper()
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": "读 " + path}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventToolCall, map[string]string{
		"id": id, "name": "fs", "arguments": `{"action":"read","path":"` + path + `"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventToolResult, map[string]any{
		"id": id, "name": "fs", "content": content, "is_error": false, "title": path,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventTurnEnd, map[string]string{"reason": "done"}); err != nil {
		t.Fatal(err)
	}
}

// 同一路径读 4 次（4 个 turn）：超预算时只保留最近一次全文，更早的收成单行。
// 基础折叠（不计入）先折掉最老两轮，去重折掉中间那份——最终全文只剩 1 份。
func TestDerive_DuplicateReadsFoldToLatest(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	big := strings.Repeat("读数内容\n", 80)
	for i, id := range []string{"c0", "c1", "c2", "c3"} {
		appendReadTurn(t, ledger, id, "src/a.go", big)
		if i == 3 {
			break // 第 4 次读取属于最近一轮（无 TurnEnd 也不影响派生）
		}
	}

	msgs, info, err := deriveMessagesWith(ledger, DeriveOptions{BudgetTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	full := 0
	for _, m := range msgs {
		if m.Role == "tool" && strings.Contains(m.Content, "读数内容") {
			full++
		}
	}
	if full != 1 {
		t.Fatalf("同一路径只应保留 1 份全文，实际 %d（info=%+v）", full, info)
	}
	if info.FoldedReads < 1 {
		t.Fatalf("重复读折叠必须计数（去重至少折 1 份）：%+v", info)
	}
}

// 对话正文是最后一级：正文折叠后最近一轮保留，旧回复变成带说明的替身。
func TestDerive_BodyFoldedLast(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	appendReadTurn(t, ledger, "r1", "src/a.go", strings.Repeat("旧读数\n", 100))
	if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": strings.Repeat("旧回复正文。", 200)}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "第二问"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventAssistantMsg, map[string]string{"text": "最新回复，不能被折叠"}); err != nil {
		t.Fatal(err)
	}

	msgs, info, err := deriveMessagesWith(ledger, DeriveOptions{BudgetTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	last := msgs[len(msgs)-1]
	if last.Content != "最新回复，不能被折叠" {
		t.Fatalf("最近一轮正文必须保留：%q（info=%+v）", last.Content, info)
	}
	if info.FoldedBodies != 1 {
		t.Fatalf("旧轮回复应折叠 1 条：%+v", info)
	}
	hasMarker := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "已折叠") {
			hasMarker = true
		}
	}
	if !hasMarker {
		t.Fatalf("旧回复的替身说明缺失（info=%+v）", info)
	}
}
