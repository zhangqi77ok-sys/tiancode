package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/session"
)

// 第 2 批：上下文预算分级折叠。
//  1. 多轮带图：超预算时旧图片不再重嵌 data URL，替换为路径说明（最近一轮仍带图）；
//  2. 旧 shell/写入回执：超预算时收成一行摘要，仍能看到命令/路径与成败结论；
//  3. 未配置预算：行为与旧版一致（图片带 data URL、shell 全文保留）。

func appendUserWithImage(t *testing.T, l *session.Ledger, text, attPath string) {
	t.Helper()
	if _, err := l.Append(session.EventUserMessage, map[string]any{
		"text": text,
		"attachments": []session.UserAttachment{{
			Kind: "image", Name: "shot.png", MediaType: "image/png",
			Path: attPath, Inline: "full", Size: 7,
		}},
	}); err != nil {
		t.Fatal(err)
	}
}

func appendAssistant(t *testing.T, l *session.Ledger, text string) {
	t.Helper()
	if _, err := l.Append(session.EventAssistantMsg, map[string]string{"text": text}); err != nil {
		t.Fatal(err)
	}
}

// appendToolPair 落一对 tool_call/tool_result（shell 命令或 fs 写入回执）。
func appendToolPair(t *testing.T, l *session.Ledger, id, name, args, content, title string) {
	t.Helper()
	if _, err := l.Append(session.EventToolCall, map[string]any{
		"id": id, "name": name, "arguments": args,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventToolResult, map[string]any{
		"id": id, "name": name, "content": content, "is_error": false, "title": title,
	}); err != nil {
		t.Fatal(err)
	}
}

// 三轮历史：每轮 1 张真实图片附件 + 1 条 shell 结果 + 1 条写回执。
func seedThreeTurnsWithImagesAndTools(t *testing.T) (*session.Ledger, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := session.OpenLedger(dir, "s-budget")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if err := os.MkdirAll(filepath.Join(dir, "att"), 0o700); err != nil {
		t.Fatal(err)
	}
	img := "att/shot.png"
	if err := os.WriteFile(filepath.Join(dir, img), []byte("PNGDATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		appendUserWithImage(t, l, "第"+string(rune('0'+i))+"轮", img)
		appendToolPair(t, l, "cmd-"+string(rune('0'+i)), "shell",
			`{"command":"go test ./..."}`, "PASS\nok all packages", "go test ./...")
		appendToolPair(t, l, "wr-"+string(rune('0'+i)), "fs",
			`{"action":"replace","path":"internal/a.go"}`, "replaced 1 occurrence", "internal/a.go")
		appendAssistant(t, l, "ok")
	}
	return l, img
}

func TestDerive_BudgetFoldsOldImages(t *testing.T) {
	l, img := seedThreeTurnsWithImagesAndTools(t)

	// 极小预算（阈值 0）= 必然折叠：验证折叠顺序与范围
	msgs, info, err := deriveMessagesWith(l, DeriveOptions{BudgetTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if info.FoldedImages < 2 {
		t.Fatalf("旧图片应被折叠（3 轮只留最近 1 轮）：%+v", info)
	}
	dataURLs, notes, noteText := 0, 0, ""
	for _, m := range msgs {
		for _, p := range m.Parts {
			if p.Type == "image_url" {
				dataURLs++
			}
			if strings.Contains(p.Text, "旧轮次已省略图像数据") {
				notes++
				noteText += p.Text
			}
		}
	}
	if dataURLs != 1 {
		t.Fatalf("最近一轮必须保留 1 张图（data URL），实际 %d", dataURLs)
	}
	if notes != 2 {
		t.Fatalf("旧图的 2 条 data URL 应替换为路径说明，实际 %d", notes)
	}
	if !strings.Contains(noteText, img) {
		t.Fatalf("折叠说明必须保留附件路径 %q：%q", img, noteText)
	}
}

func TestDerive_BudgetFoldsOldToolResults(t *testing.T) {
	l, _ := seedThreeTurnsWithImagesAndTools(t)

	msgs, info, err := deriveMessagesWith(l, DeriveOptions{BudgetTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 折叠范围 = turn < totalTurns-keepFullToolTurns = 只折第一轮（最近两轮保留）
	if info.FoldedTools < 2 {
		t.Fatalf("两轮以前的 shell+写回执应被折叠（留最近 2 轮）：%+v", info)
	}

	// 旧 shell 摘要：命令与结论可见；旧写回执：路径可见
	var oldShell, oldWrite, lastWrite string
	for _, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		switch m.ToolCallID {
		case "cmd-1":
			oldShell = m.Content
		case "wr-1":
			oldWrite = m.Content
		case "wr-3":
			lastWrite = m.Content
		}
	}
	if !strings.Contains(oldShell, "go test ./...") || !strings.Contains(oldShell, "已完成") {
		t.Fatalf("旧 shell 摘要必须含命令与结论：%q", oldShell)
	}
	if strings.Contains(oldShell, "PASS") {
		t.Fatalf("旧 shell 全文应已省略：%q", oldShell)
	}
	if !strings.Contains(oldWrite, "internal/a.go") {
		t.Fatalf("旧写回执摘要必须含路径：%q", oldWrite)
	}
	if lastWrite != "replaced 1 occurrence" {
		t.Fatalf("最近一轮的写回执必须保留全文：%q", lastWrite)
	}
}

// 第 6 批：从这条用户消息重跑（fork）后，被丢弃区间的旧工具结果不再喂给模型；
// 目标用户消息本身也被丢弃（重跑重新落一条同文本消息，不得出现两条重复输入）。
func TestDerive_RerunDropsForkedHistory(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	u1, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "第一问"})
	if err != nil {
		t.Fatal(err)
	}
	appendToolPair(t, ledger, "c1", "shell", `{"command":"go build"}`, "OLD-MARKER ok", "go build")
	appendAssistant(t, ledger, "第一次回答")

	// 从这条用户消息重跑：分叉丢弃 [u1.seq, fork]
	if _, err := ledger.Append(session.EventFork, map[string]any{"from_seq": u1.Seq()}); err != nil {
		t.Fatal(err)
	}
	// 重跑：新的同文本用户消息 + 新结果
	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "第一问"}); err != nil {
		t.Fatal(err)
	}
	appendToolPair(t, ledger, "c2", "shell", `{"command":"go build"}`, "NEW-MARKER ok", "go build")
	appendAssistant(t, ledger, "第二次回答")

	msgs, _, err := deriveMessagesWith(ledger, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, m := range msgs {
		joined += m.Content
		for _, p := range m.Parts {
			joined += p.Text
		}
	}
	if strings.Contains(joined, "OLD-MARKER") {
		t.Fatalf("被重跑丢弃的旧工具结果不得再喂给模型：%s", joined)
	}
	if !strings.Contains(joined, "NEW-MARKER") {
		t.Fatalf("重跑后的新结果必须在历史里：%s", joined)
	}
	if n := strings.Count(joined, "第一问"); n != 1 {
		t.Fatalf("重跑后用户消息应恰好一条（旧的被丢弃），实际 %d", n)
	}
}

// 第 6 批：用户手动写入（EventUserEdit）在派生历史里有一句"已应用过"。
func TestDerive_UserEditNoteReachesModel(t *testing.T) {
	ledger, _ := newTestLedger(t)
	defer ledger.Close()

	if _, err := ledger.Append(session.EventUserMessage, map[string]string{"text": "改一下"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Append(session.EventUserEdit, map[string]any{
		"path": "internal/a.go", "is_new": false, "bytes": 12, "diff": "-x\n+y\n",
	}); err != nil {
		t.Fatal(err)
	}
	appendAssistant(t, ledger, "好的")

	msgs, _, err := deriveMessagesWith(ledger, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "用户手动") && strings.Contains(m.Content, "internal/a.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("模型上下文必须有一句「已应用过」：%+v", msgs)
	}
}

func TestDerive_NoBudgetKeepsEverything(t *testing.T) {
	l, _ := seedThreeTurnsWithImagesAndTools(t)

	msgs, info, err := deriveMessagesWith(l, DeriveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if info.FoldedImages != 0 || info.FoldedTools != 0 {
		t.Fatalf("未配置预算不得因预算折叠：%+v", info)
	}
	dataURLs := 0
	for _, m := range msgs {
		for _, p := range m.Parts {
			if p.Type == "image_url" {
				dataURLs++
			}
		}
	}
	if dataURLs != 3 {
		t.Fatalf("未配置预算时三张图都应带 data URL，实际 %d", dataURLs)
	}
	for _, m := range msgs {
		if m.ToolCallID == "cmd-1" && !strings.Contains(m.Content, "PASS") {
			t.Fatalf("未配置预算时旧 shell 结果保留全文：%q", m.Content)
		}
	}
}
