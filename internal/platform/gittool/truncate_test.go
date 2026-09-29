package gittool

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 审计#8：truncate 走 truncateToBytes——按字节切中文会出非法 UTF-8（乱码）。
func TestTruncateUTF8Safe(t *testing.T) {
	// 中文字符每字 3 字节：outputLimit 处切一刀必然落在字符中间
	s := strings.Repeat("编", outputLimit/3+10)
	out := truncate(s)
	if !utf8.ValidString(out) {
		t.Fatalf("截断结果含非法 UTF-8（中文被切半）：%q…", out[:20])
	}
	if !strings.HasSuffix(out, "[truncated]") {
		t.Fatalf("截断结果应带标注：%q", out[len(out)-20:])
	}
	if !utf8.ValidString(truncateToBytes("中文内容", 4)) {
		t.Fatal("truncateToBytes 应回退到 UTF-8 边界")
	}
}
