package agent

import (
	"strings"
	"testing"

	"tiancode/internal/core/session"
)

// appendFSPair 追加一对 tool_call / tool_result 账本事件。
func appendFSPair(t *testing.T, l *session.Ledger, id, name, args, content, title string) {
	t.Helper()
	if _, err := l.Append(session.EventToolCall, map[string]string{"id": id, "name": name, "arguments": args}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventToolResult, map[string]any{
		"id": id, "name": name, "content": content, "is_error": false, "title": title,
	}); err != nil {
		t.Fatal(err)
	}
}

func appendTurn(t *testing.T, l *session.Ledger, user string) {
	t.Helper()
	if _, err := l.Append(session.EventUserMessage, map[string]string{"text": user}); err != nil {
		t.Fatal(err)
	}
}

func toolContent(t *testing.T, l *session.Ledger, opt DeriveOptions, id string) string {
	t.Helper()
	msgs, _, err := deriveMessagesWith(l, opt)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == id {
			return m.Content
		}
	}
	t.Fatalf("派生结果里没有 %s", id)
	return ""
}

// 上一轮读过的文件在下一轮保留全文（此前跨轮一律截到 4KB，模型只能重读）；
// 其它工具结果仍按 4KB 模型侧上限截断（C-AGT-2 不变）。
func TestDerive_RecentFileReadKeptAcrossTurns(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()
	body := strings.Repeat("func a() {}\n", 2000) // ~24KB
	appendTurn(t, l, "读一下 a.go")
	appendFSPair(t, l, "r1", "fs", `{"action":"read","path":"a.go"}`, body, "a.go")
	appendFSPair(t, l, "s1", "shell", `{"action":"run","command":"go test"}`, body, "go test")
	if _, err := l.Append(session.EventAssistantMsg, map[string]string{"text": "读完了"}); err != nil {
		t.Fatal(err)
	}
	appendTurn(t, l, "把刚才那个函数改一下")

	if got := toolContent(t, l, DeriveOptions{}, "r1"); got != body {
		t.Fatalf("上一轮的 fs.read 应保留全文：len=%d want %d", len(got), len(body))
	}
	if got := toolContent(t, l, DeriveOptions{}, "s1"); len(got) > toolResultModelLimit+256 {
		t.Fatalf("非读取类结果仍应按 4KB 截断：len=%d", len(got))
	}
	// 段内重折叠用的 CompactReads：读全文也让出来
	if got := toolContent(t, l, DeriveOptions{CompactReads: true}, "r1"); len(got) > toolResultModelLimit+256 {
		t.Fatalf("CompactReads 下 fs.read 应按 4KB 截断：len=%d", len(got))
	}
}

// 读之后又被成功 write/replace 的文件：保留的读全文前注明"修改前的内容"。
func TestDerive_StaleReadMarked(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()
	appendTurn(t, l, "改 a.go")
	appendFSPair(t, l, "r1", "fs", `{"action":"read","path":"a.go"}`, "old body", "a.go")
	appendFSPair(t, l, "r2", "fs", `{"action":"read","path":"b.go"}`, "b body", "b.go")
	appendFSPair(t, l, "w1", "fs", `{"action":"replace","path":"a.go","target":"old","replacement":"new"}`, "replaced 1", "a.go")
	appendTurn(t, l, "继续")

	if got := toolContent(t, l, DeriveOptions{}, "r1"); !strings.HasPrefix(got, staleReadNote) || !strings.Contains(got, "old body") {
		t.Fatalf("被改过的文件读取应带修改前标注：%q", got)
	}
	if got := toolContent(t, l, DeriveOptions{}, "r2"); got != "b body" {
		t.Fatalf("没被改过的文件不应标注：%q", got)
	}
}

// 更早的轮次照旧被基础折叠收成单行（保留全文只覆盖最近 keepFullToolTurns 轮）。
func TestDerive_OldFileReadStillFolded(t *testing.T) {
	l, _ := newTestLedger(t)
	defer l.Close()
	body := strings.Repeat("x", 20<<10)
	appendTurn(t, l, "第一轮")
	appendFSPair(t, l, "r1", "fs", `{"action":"read","path":"a.go"}`, body, "a.go")
	appendTurn(t, l, "第二轮")
	appendTurn(t, l, "第三轮")
	appendTurn(t, l, "第四轮")
	if got := toolContent(t, l, DeriveOptions{}, "r1"); len(got) > 512 {
		t.Fatalf("三轮以前的读取应已折成单行：len=%d", len(got))
	}
}
