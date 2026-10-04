// 前台命令输出有界（0.0.25 引入，0.0.26 重构）：超限时头尾保留 + **唯一**截断标记
// （缓冲滚动丢弃与解码后截断的丢弃量合并计数），且绝不切碎 UTF-8。
package shelltool

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestForegroundOutput_UntouchedUnderLimit(t *testing.T) {
	buf := newBoundedBuffer(defaultBGLogLimit)
	buf.Write([]byte("go test ok"))
	out, truncated := buf.Foreground(maxForegroundOutput)
	if truncated || out != "go test ok" {
		t.Fatalf("未超限必须原样返回且不算截断：%q truncated=%v", out, truncated)
	}
}

// 回归（0.0.26）：丢弃量必须合并进唯一标记。旧实现"缓冲截一次 + 对截断结果再截
// 一次"会把 5MB 输出的真实丢弃量显示成 49 字节——模型误以为几乎没丢。
func TestForegroundOutput_SingleMarkerWithTrueDropped(t *testing.T) {
	buf := newBoundedBuffer(defaultBGLogLimit)
	buf.Write([]byte(strings.Repeat("a", 5*1024*1024)))
	out, truncated := buf.Foreground(maxForegroundOutput)
	if !truncated {
		t.Fatal("5MB 输出必须报截断")
	}
	if n := strings.Count(out, "[truncated"); n != 1 {
		t.Fatalf("必须只有唯一截断标记，实际 %d 个", n)
	}
	// 标记里的丢弃量必须是真实量级（数百万字节级）：旧 bug 把它显示成几十字节。
	// 精确值随 HeadTail 头尾配比浮动，这里卡区间：丢弃的至少是 5MB 减去保留预算。
	m := markerDropped(t, out)
	if m < 5*1024*1024-128*1024 || m > 5*1024*1024 {
		t.Fatalf("丢弃量应接近 5MB（保留头尾之外全部丢弃），实际 %d", m)
	}
}

// markerDropped 从唯一的 [truncated: N middle bytes omitted] 标记里解析 N。
func markerDropped(t *testing.T, out string) int {
	t.Helper()
	i := strings.Index(out, "[truncated: ")
	if i < 0 {
		t.Fatalf("缺截断标记：%q", out[max(0, len(out)-160):])
	}
	rest := out[i+len("[truncated: "):]
	j := strings.Index(rest, " ")
	if j < 0 {
		t.Fatalf("截断标记格式异常：%q", out[i:])
	}
	n := 0
	for _, c := range rest[:j] {
		if c < '0' || c > '9' {
			t.Fatalf("丢弃量不是纯数字：%q", rest[:j])
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// GBK 输出解码后膨胀（2 字节→3 字节），解码整串可能超过按原始字节算的预算——
// Foreground 必须在解码整串上做唯一一次截断（这是它区别于 String() 的存在理由），
// 且切点不落进多字节字符。
func TestForegroundOutput_GBKExpandStillBounded(t *testing.T) {
	raw, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(strings.Repeat("中文输出", 40000)))
	if err != nil {
		t.Fatal(err)
	}
	buf := newBoundedBuffer(defaultBGLogLimit)
	buf.Write(raw)
	out, truncated := buf.Foreground(maxForegroundOutput)
	if !truncated {
		t.Fatalf("GBK 解码膨胀后应触发截断（原始 %d 字节）", len(raw))
	}
	// 限值 + 唯一标记（几十字节）的余量
	if len(out) > maxForegroundOutput+128 {
		t.Fatalf("截断后超界：%d 字节", len(out))
	}
	if n := strings.Count(out, "[truncated"); n != 1 {
		t.Fatalf("必须只有唯一截断标记，实际 %d 个", n)
	}
	if !utf8.ValidString(out) || strings.Contains(out, "\uFFFD") {
		t.Fatal("截断产生了非法 UTF-8/替换符（切在多字节字符中间）")
	}
}

// 端到端：真跑一条超长输出命令，验证工具回执带截断标记与出路提示。
// 注意循环变量是 %i 不是 %%i：cmd /c 是命令行上下文，%%i 是批处理文件语法——
// 旧写法让命令秒退、输出仅 52 字节，测试静默 skip，截断路径从未真正跑到过。
func TestRun_ForegroundOutputBounded(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd for /L 语法仅 Windows")
	}
	tool := New(Options{Root: t.TempDir()})
	res, err := tool.Execute(context.Background(), args(t, map[string]any{
		"action":  "run",
		"command": "for /L %i in (1,1,60000) do @echo 这是一行很长的中文输出用于测试截断%i",
	}))
	if err != nil {
		t.Fatalf("机制错误：%v", err)
	}
	if len(res.Content) <= maxForegroundOutput {
		t.Fatalf("输出应已超限（实际 %d 字节），截断路径未触发", len(res.Content))
	}
	if n := strings.Count(res.Content, "[truncated"); n != 1 {
		t.Fatalf("必须只有唯一截断标记，实际 %d 个", n)
	}
	if !strings.Contains(res.Content, "被截断") || !strings.Contains(res.Content, "重定向") {
		t.Fatalf("缺中文截断说明/出路提示，输出尾部：%q", res.Content[max(0, len(res.Content)-160):])
	}
	// 限值 + 唯一标记 + 出路提示的余量
	if len(res.Content) > maxForegroundOutput+256 {
		t.Fatalf("超限输出未被有界化：%d 字节", len(res.Content))
	}
	if !utf8.ValidString(res.Content) {
		t.Fatal("最终输出含非法 UTF-8")
	}
}
