package tools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 0.0.06 用户要求：截断必须"保留头部 + 保留尾部 + 中间标注丢了多少字节"——
// 测试日志的 FAIL 汇总与 git diff 末尾的文件名都在尾部，只留头部会把模型带偏。
func TestHeadTail_KeepsHeadAndTailWithMarker(t *testing.T) {
	head := strings.Repeat("H", 1000)
	tail := "FAIL: boom at the very end"
	s := head + strings.Repeat("x", 6000) + tail

	out := HeadTail(s, 2048)
	if len(out) > 2048+200 { // 预算 + 标注行余量
		t.Fatalf("输出超预算：%d bytes", len(out))
	}
	if !strings.HasPrefix(out, head[:100]) {
		t.Fatal("头部丢失")
	}
	if !strings.Contains(out, tail) {
		t.Fatalf("尾部丢失（FAIL 汇总被截没）：%s", out[len(out)-120:])
	}
	if !strings.Contains(out, "middle bytes omitted") {
		t.Fatal("必须标注中间丢失字节数")
	}
	// 标注里的数字 = 精确丢失字节数（头 819 + 尾 1229 之外的全部 = 4978）
	if !strings.Contains(out, "4978") {
		t.Fatalf("标注应包含精确丢失字节数 4978：%s", out[strings.Index(out, "truncated"):strings.Index(out, "truncated")+60])
	}
}

// 尾部权重大于头部：2/5 头、3/5 尾。
func TestHeadTail_TailHeavierThanHead(t *testing.T) {
	s := strings.Repeat("A", 5000) + strings.Repeat("B", 5000)
	out := HeadTail(s, 1000)
	headLen := strings.Index(out, "\n...[truncated")
	if headLen < 0 {
		t.Fatalf("缺标注：%s", out)
	}
	tailLen := len(out) - (strings.LastIndex(out, "]...") + 4) - 1
	if tailLen <= headLen {
		t.Fatalf("尾部保留应多于头部：head=%d tail=%d", headLen, tailLen)
	}
}

// 不超限原样返回；UTF-8 边界不切半。
func TestHeadTail_IdempotentAndUTF8Safe(t *testing.T) {
	if got := HeadTail("short", 100); got != "short" {
		t.Fatalf("不超限应原样返回：%q", got)
	}
	s := strings.Repeat("编", 2000) + strings.Repeat("字", 2000) + "TAIL-MARK-尾部"
	out := HeadTail(s, 3000)
	if !utf8.ValidString(out) {
		t.Fatal("截断结果含非法 UTF-8")
	}
	if !strings.Contains(out, "TAIL-MARK-尾部") {
		t.Fatal("中段附近的关键尾部内容丢失")
	}
}

// 流式版：逐行写入，尾部滚动；语义与字符串版一致。
func TestHeadTailWriter_StreamingRing(t *testing.T) {
	w := NewHeadTailWriter(1200)
	head := strings.Repeat("H", 400)
	w.Write([]byte(head + "\n"))
	for i := 0; i < 400; i++ {
		w.Write([]byte("filler-line-\n"))
	}
	tail := "FAIL: last line matters"
	w.Write([]byte(tail + "\n"))

	out := w.String()
	if !strings.Contains(out, "middle bytes omitted") {
		t.Fatalf("缺中间标注：%q", out)
	}
	if !strings.Contains(out, tail) {
		t.Fatal("最后的 FAIL 行被滚动丢弃")
	}
	if !strings.Contains(out, head) {
		t.Fatal("头部丢失")
	}
	if !utf8.ValidString(out) {
		t.Fatal("非法 UTF-8")
	}
}

// 流式版在极限滚动下不 panic 且产出合法 UTF-8（环形边界）。
func TestHeadTailWriter_RingBoundaryStress(t *testing.T) {
	w := NewHeadTailWriter(300)
	rnd := "字" // 3 字节：迫使环形边界落在多字节字符中间
	for i := 0; i < 500; i++ {
		w.Write([]byte(rnd))
	}
	out := w.String()
	if !utf8.ValidString(out) {
		t.Fatalf("环形滚动产出非法 UTF-8：%q…", out[:30])
	}
}
