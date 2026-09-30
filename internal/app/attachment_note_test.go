package app

import (
	"strings"
	"testing"
	"time"

	"tiancode/internal/core/session"
)

// 0.0.21 实机反馈"发文件后一直没输出"：那轮 115KB 文本被整体内联、请求体到 510KB，
// 上游 90 秒零字节、看门狗收掉——但原文案只让人去查代理，看不出"这轮请求特别大"。
// 这里锁住"超时文案必须带本轮内联附件的体量事实"。
func TestInlineAttachmentNote(t *testing.T) {
	// 没有内联附件：不补任何话（普通超时文案保持原样）
	if got := inlineAttachmentNote(nil); got != "" {
		t.Fatalf("无附件不该有补充：%q", got)
	}
	// 只附路径的附件不进请求体，同样不补
	pathOnly := []session.UserAttachment{{Kind: "file", Name: "big.bin", Inline: "path", Size: 1 << 20}}
	if got := inlineAttachmentNote(pathOnly); got != "" {
		t.Fatalf("只附路径的附件不该计数：%q", got)
	}
	// 内联附件：个数 + 体积（KB）都要出现
	atts := []session.UserAttachment{
		{Kind: "file", Name: "#1065.txt", Inline: "full", Size: 117914},
		{Kind: "image", Name: "shot.png", Inline: "none", Size: 20000},
	}
	got := inlineAttachmentNote(atts)
	if !strings.Contains(got, "1 个附件") || !strings.Contains(got, "115KB") {
		t.Fatalf("要写清个数与体积：%q", got)
	}
}

// 看门狗超时文案：有事实就带上，没有就一字不多（旧文案不能被改写）。
func TestWatchdogTimeoutMessageCarriesNote(t *testing.T) {
	plain := newZeroEventWatch(90*time.Second, "")
	if msg := plain.timeoutMessage(); strings.Contains(msg, "附件") {
		t.Fatalf("无事实时不该出现附件字样：%q", msg)
	}
	noted := newZeroEventWatch(90*time.Second, "本轮有 1 个附件的内容被内联进请求体（共 115KB）")
	msg := noted.timeoutMessage()
	if !strings.Contains(msg, "响应超时") || !strings.Contains(msg, "本轮有 1 个附件") {
		t.Fatalf("超时文案要带上体量事实：%q", msg)
	}
}
