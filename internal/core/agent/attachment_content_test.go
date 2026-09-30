package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
)

// 0.0.25：附件轮的 Content 与 Parts 必须**都**有内容，且同源。
// 为什么（实机反馈："对话上传文件或图片后发出去没有回复"）：derive 此前只填 Parts、
// Content 留空，而 Anthropic 与 Codex 两个适配器只读 Content——这条用户消息到了那边
// 等于没发出去。这里锁住派生层的形状，适配器侧的形状另有三协议用例。
func TestDerive_AttachmentFillsContentAndParts(t *testing.T) {
	dir := t.TempDir()
	l, err := session.OpenLedger(dir, "s-att")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := os.MkdirAll(filepath.Join(dir, "att"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "att", "shot.png"), []byte("PNGDATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "att", "app.log"), []byte("ERROR 第 3 行"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(session.EventUserMessage, map[string]any{
		"text": "看这张图和日志",
		"attachments": []session.UserAttachment{
			{Kind: "image", Name: "shot.png", MediaType: "image/png", Path: "att/shot.png", Inline: "full", Size: 7},
			{Kind: "file", Name: "app.log", MediaType: "text/plain", Path: "att/app.log", Inline: "full", Size: 13},
		},
	}); err != nil {
		t.Fatal(err)
	}

	msgs, err := deriveMessages(l)
	if err != nil {
		t.Fatal(err)
	}
	var user *llm.Message
	for i := range msgs {
		if msgs[i].Role == "user" {
			user = &msgs[i]
		}
	}
	if user == nil {
		t.Fatal("没有派生出发给模型的用户消息")
	}
	// Content：用户原文 + 内联文件正文 + 图片只写名字与"图像见多模态部分"
	for _, want := range []string{"看这张图和日志", "ERROR 第 3 行", "shot.png", "图像见多模态部分"} {
		if !strings.Contains(user.Content, want) {
			t.Fatalf("Content 缺 %q：%q", want, user.Content)
		}
	}
	// Content 里绝不能有图片 base64（"PNGDATA" 的 base64 是 UE5HREFUQQ==）
	if strings.Contains(user.Content, "UE5HREFUQQ") || strings.Contains(user.Content, "base64") {
		t.Fatalf("图片 base64 不得进 Content：%q", user.Content)
	}

	// Parts：文字段 + 动画片段的 data URL（带文件名，供 Codex 的文字说明）
	textParts, imgs := 0, 0
	var joined strings.Builder
	for _, p := range user.Parts {
		switch p.Type {
		case "image_url":
			imgs++
			if !strings.HasPrefix(p.ImageURL, "data:image/png;base64,") {
				t.Fatalf("图片必须是内联 data URL（本工具不转公网链接）：%.40s", p.ImageURL)
			}
			if p.Name != "shot.png" {
				t.Fatalf("图片片段要带原始文件名：%q", p.Name)
			}
		case "text":
			textParts++
			joined.WriteString(p.Text)
		}
	}
	if imgs != 1 || textParts == 0 {
		t.Fatalf("Parts 形状不对：imgs=%d text=%d", imgs, textParts)
	}
	// 同源：Parts 的文字拼起来必须等于 Content（两处各写一套文本迟早漂移）
	if joined.String() != user.Content {
		t.Fatalf("Parts 文字与 Content 必须同源：\nparts=%q\ncontent=%q", joined.String(), user.Content)
	}
}

// 只附路径（未内联）的文件：Content 里同样要有可执行说明（不给模型看内容，但要说清去哪读）。
func TestDerive_AttachmentPathOnlyMentionsPath(t *testing.T) {
	dir := t.TempDir()
	l, err := session.OpenLedger(dir, "s-att2")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := l.Append(session.EventUserMessage, map[string]any{
		"text": "这份日志你看看",
		"attachments": []session.UserAttachment{
			{Kind: "file", Name: "big.log", MediaType: "text/plain", Path: "D:\\logs\\big.log", Inline: "path", Size: 1 << 20},
		},
	}); err != nil {
		t.Fatal(err)
	}
	msgs, err := deriveMessages(l)
	if err != nil {
		t.Fatal(err)
	}
	var user *llm.Message
	for i := range msgs {
		if msgs[i].Role == "user" {
			user = &msgs[i]
		}
	}
	if user == nil {
		t.Fatal("没有派生出发给模型的用户消息")
	}
	if !strings.Contains(user.Content, `D:\logs\big.log`) || !strings.Contains(user.Content, "未内联") {
		t.Fatalf("未内联附件必须在 Content 里写明路径与未内联：%q", user.Content)
	}
	if len(user.Parts) == 0 || user.Parts[0].Text != user.Content {
		t.Fatalf("纯文件附件也必须有同源文字段：%+v", user.Parts)
	}
}
