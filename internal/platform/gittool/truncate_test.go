package gittool

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 审计#8：truncate 按字节切中文会出非法 UTF-8（乱码）——头尾保留实现同样要守。
// 0.0.06：截断改为头尾保留，尾部（diff 末尾的文件块）必须可见。
func TestTruncateUTF8Safe(t *testing.T) {
	// 中文字符每字 3 字节：切点必然落在字符中间
	s := strings.Repeat("编", outputLimit/3+10)
	out := truncate(s)
	if !utf8.ValidString(out) {
		t.Fatalf("截断结果含非法 UTF-8（中文被切半）：%q…", out[:20])
	}
	if !strings.Contains(out, "middle bytes omitted") {
		t.Fatalf("截断结果应标注中间丢失字节数：%q", out[len(out)-60:])
	}
	if !utf8.ValidString(truncateToBytes("中文内容", 4)) {
		t.Fatal("truncateToBytes 应回退到 UTF-8 边界")
	}
}

// 0.0.06：大 diff 截断后，末尾的文件名/变更块不能被截没——只留头部的旧实现
// 会让模型以为后面文件的改动不存在。
func TestTruncate_KeepsTailFileNames(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/first.txt b/first.txt\n")
	for i := 0; i < outputLimit/16; i++ {
		b.WriteString("+changed-line-in-middle\n") // ~24B/行，填充到远超 64KiB
	}
	b.WriteString("diff --git a/zzz-last-file.txt b/zzz-last-file.txt\n+the actual last change")
	out := truncate(b.String())
	if len(out) >= len(b.String()) {
		t.Fatal("测试前提：内容必须真的超限")
	}
	if !strings.Contains(out, "zzz-last-file.txt") || !strings.Contains(out, "the actual last change") {
		t.Fatalf("末尾文件与变更被截没：%s", out[len(out)-200:])
	}
	if !strings.Contains(out, "first.txt") {
		t.Fatal("头部文件清单丢失")
	}
}
