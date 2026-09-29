package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"tiancode/internal/core/session"
)

// 0.0.09 油表治理：非最近轮次的成功只读结果在模型上下文里收成单行（丢弃旧
// 读数，不是摘要——账本原文不动）；最近 2 轮保持全文；写类与错误永远全文。
func TestDerive_FoldsOldReadOnlyResults(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	big := strings.Repeat("旧读数内容行\n", 100) // 每轮读到的全文（>折叠单行）
	appendTurn := func(id string, user, tool string, name, action string, isErr bool) {
		if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": user}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(session.EventToolCall, map[string]string{
			"id": id, "name": name, "arguments": `{"action":"` + action + `"}`,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(session.EventToolResult, map[string]any{
			"id": id, "name": name, "content": tool, "is_error": isErr, "title": "src/a.go",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(session.EventTurnEnd, map[string]string{"reason": "done"}); err != nil {
			t.Fatal(err)
		}
	}
	appendTurn("c1", "第一问", big, "fs", "read", false)              // turn0（旧 → 折叠）
	appendTurn("c2", "第二问", big, "fs", "read", false)              // turn1（最近 2 轮内 → 全文）
	appendTurn("c3", "第三问", "写入完成 12 bytes", "fs", "write", false) // turn2（写类 → 全文）
	appendTurn("c4", "第四问", big, "search", "", false)              // turn3（当前 → 全文）
	// 旧轮次的错误结果：不折叠
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "第五问"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventToolCall, map[string]string{
		"id": "c5", "name": "fs", "arguments": `{"action":"read"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventToolResult, map[string]any{
		"id": "c5", "name": "fs", "content": "file not found", "is_error": true, "title": "src/x.go",
	}); err != nil {
		t.Fatal(err)
	}

	msgs, err := deriveMessages(ledger)
	if err != nil {
		t.Fatal(err)
	}
	toolMsgs := map[string]string{}
	for _, m := range msgs {
		if m.Role == "tool" {
			toolMsgs[m.ToolCallID] = m.Content
		}
	}

	// c1/c2（超出最近 2 轮的旧轮）：折叠单行
	if got := toolMsgs["c1"]; !strings.Contains(got, "fs src/a.go → 已读") || strings.Contains(got, "旧读数内容行") {
		t.Fatalf("旧轮只读结果应折叠为单行：%q", got)
	}
	if got := toolMsgs["c2"]; !strings.Contains(got, "fs src/a.go → 已读") {
		t.Fatalf("turn1 超出最近 2 轮，应折叠：%q", got)
	}
	// c4（turn3 = 最近 2 轮内）：全文
	if got := toolMsgs["c4"]; !strings.Contains(got, "旧读数内容行") {
		t.Fatalf("最近 2 轮应保持全文：%q", got)
	}
	// c3（写类）：全文
	if got := toolMsgs["c3"]; got != "写入完成 12 bytes" {
		t.Fatalf("写类结果永不折叠：%q", got)
	}
	// c5（错误）：全文
	if got := toolMsgs["c5"]; got != "file not found" {
		t.Fatalf("错误结果永不折叠：%q", got)
	}
}

// 0.0.10：shell 结果无论退出码都永不折叠——go test 的失败栈几轮后仍要完整可见
// （退出码非 0 时 IsError=false，仅凭 IsError 判定会把失败收成一行）。
// 旧的成功 read 仍被收成一行（对照）。
func TestDerive_ShellResultsNeverFolded(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	shellFail := strings.Repeat("ok\n", 50) + "--- FAIL: TestX\n    expected 1, got 2"
	shellOK := "build succeeded"
	readBig := strings.Repeat("readline\n", 100)

	appendTurn := func(id, user, name, action, content string, isErr bool) {
		if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": user}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(session.EventToolCall, map[string]string{
			"id": id, "name": name, "arguments": `{"action":"` + action + `"}`,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(session.EventToolResult, map[string]any{
			"id": id, "name": name, "content": content, "is_error": isErr, "title": "go test ./...",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Append(session.EventTurnEnd, map[string]string{"reason": "done"}); err != nil {
			t.Fatal(err)
		}
	}
	appendTurn("s0", "先读", "fs", "read", readBig, false)  // turn0（旧 → 折叠）
	appendTurn("s1", "跑测试", "shell", "", shellFail, true) // turn1（旧失败 shell → 保留）
	appendTurn("s2", "再跑", "shell", "", shellOK, false)   // turn2（旧成功 shell → 保留）
	appendTurn("s3", "读文件", "fs", "read", readBig, false) // turn3（最近 2 轮 → 全文）
	appendTurn("s4", "总结", "fs", "read", "tail info", false)

	msgs, err := deriveMessages(ledger)
	if err != nil {
		t.Fatal(err)
	}
	toolMsgs := map[string]string{}
	for _, m := range msgs {
		if m.Role == "tool" {
			toolMsgs[m.ToolCallID] = m.Content
		}
	}
	// 旧 shell 失败：FAIL 栈仍在（头加尾保留）
	if got := toolMsgs["s1"]; !strings.Contains(got, "FAIL: TestX") || !strings.Contains(got, "expected 1, got 2") {
		t.Fatalf("旧 shell 失败结果不得折叠（FAIL 栈必须可见）：%q", got)
	}
	// 旧 shell 成功：全文保留
	if got := toolMsgs["s2"]; got != shellOK {
		t.Fatalf("旧 shell 成功结果不得折叠：%q", got)
	}
	// 旧成功 read：收成一行
	if got := toolMsgs["s0"]; strings.Contains(got, "readline") {
		t.Fatalf("旧成功 read 应收成一行：%q", got)
	}
	// 最近 2 轮内的 read：全文
	if got := toolMsgs["s3"]; !strings.Contains(got, "readline") {
		t.Fatalf("最近 2 轮内 read 应保持全文：%q", got)
	}
}

// 折叠只影响模型投影：账本原文不动（重放两次结果一致，且账本文件未改写）。
func TestDerive_FoldDoesNotMutateLedger(t *testing.T) {
	ledger, dir := newTestLedger(t)
	defer ledger.Close()

	big := strings.Repeat("x", 5000)
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "q1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventToolCall, map[string]string{
		"id": "c1", "name": "fs", "arguments": `{"action":"read"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventToolResult, map[string]any{
		"id": "c1", "name": "fs", "content": big, "is_error": false, "title": "big.txt",
	}); err != nil {
		t.Fatal(err)
	}
	// 再来一个 turn 让 c1 变"旧"
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "q2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventToolCall, map[string]string{
		"id": "c2", "name": "fs", "arguments": `{"action":"read"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventToolResult, map[string]any{
		"id": "c2", "name": "fs", "content": big, "is_error": false, "title": "big.txt",
	}); err != nil {
		t.Fatal(err)
	}
	// 第三个 turn：让 c1（turn0）落到"最近 2 轮"之外
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "q3"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventTurnEnd, map[string]string{"reason": "done"}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		msgs, err := deriveMessages(ledger)
		if err != nil {
			t.Fatal(err)
		}
		var c1, c2 string
		for _, m := range msgs {
			if m.Role != "tool" {
				continue
			}
			if m.ToolCallID == "c1" {
				c1 = m.Content
			}
			if m.ToolCallID == "c2" {
				c2 = m.Content
			}
		}
		if strings.Contains(c1, strings.Repeat("x", 100)) {
			t.Fatalf("第 %d 次投影：旧轮结果应折叠", i)
		}
		if !strings.Contains(c2, strings.Repeat("x", 100)) {
			t.Fatalf("第 %d 次投影：最近 2 轮内的 c2 应保持全文", i)
		}
	}
	// 账本原文仍可全量重放（Replay 语义未受影响）
	l2, err := session.OpenLedger(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	sawBig := false
	err = l2.Replay(func(ev session.Event) error {
		if ev.Kind() == session.EventToolResult {
			var p struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(ev.Data(), &p); err != nil {
				return err
			}
			if len(p.Content) == 5000 {
				sawBig = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawBig {
		t.Fatal("账本原文必须完整保留（折叠只在投影层）")
	}
}
