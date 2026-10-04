package app

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 0.0.30 用户审查 R2 的回归锁：提交说明按字节截断不得切坏中文。
//
// 旧实现：len(msg) > 500 判断 + msg[:500] 直切。中文一字三字节，约 166 字
// 就会切在半个 rune 上——说明变乱码，而 commit -m 照单全收，乱码永久进提交历史。

func TestTruncateUTF8_NeverSplitsRune(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
	}{
		{"纯中文正好跨界", strings.Repeat("字", 200), 500},
		{"中英混排", strings.Repeat("中文abc", 200), 501},
		{"emoji", strings.Repeat("🙂", 100), 105},
		{"不超限原样返回", "短说明", 500},
	}
	for _, c := range cases {
		got := truncateUTF8(c.in, c.n)
		if !utf8.ValidString(got) {
			t.Fatalf("%s: 截断产出非法 UTF-8", c.name)
		}
		if len(got) > c.n {
			t.Fatalf("%s: 截断后仍超上限：%d > %d", c.name, len(got), c.n)
		}
		if len(c.in) <= c.n && got != c.in {
			t.Fatalf("%s: 未超限却被改动", c.name)
		}
	}
}

func TestSuggestCommitPrompt_ListsUntrackedFiles(t *testing.T) {
	// R1 的另一半：未跟踪文件不在 diff 里（diff 只讲已跟踪文件的增删），不点名
	// 的话模型看不见新文件——而 add -A 会把它们收进同一次提交。
	p := suggestCommitPrompt("## main", "diff --git a/a.go b/a.go", []string{"new/新文件.go", "b.txt"})
	if !strings.Contains(p, "new/新文件.go") || !strings.Contains(p, "b.txt") {
		t.Fatalf("提示词未列未跟踪文件：%s", p)
	}
	if !strings.Contains(p, "新增文件") {
		t.Fatalf("提示词未标注这是新增文件：%s", p)
	}
	// 没有未跟踪文件时不要留空标题
	p2 := suggestCommitPrompt("## main", "diff", nil)
	if strings.Contains(p2, "新增文件") {
		t.Fatalf("无未跟踪文件却出现该段：%s", p2)
	}
}

// TestCommitMsgTruncation_KeepsChineseIntact 端到端锁住"说明不会被切坏"：
// 超长中文说明走截断后必须仍是合法 UTF-8 且长度受限。
func TestCommitMsgTruncation_KeepsChineseIntact(t *testing.T) {
	long := strings.Repeat("这是一个很长的中文提交说明", 100) // 1300 字节
	got := truncateUTF8(long, commitMsgMaxBytes)
	if !utf8.ValidString(got) {
		t.Fatal("超长中文说明被切坏")
	}
	// 500 不是 3 的整数倍 → 必须回退到最近的 UTF-8 边界（最多退 3 字节：
	// 一个 rune 最长 4 字节，切点最坏落在它中间）
	if len(got) > commitMsgMaxBytes {
		t.Fatalf("截断后仍超上限：%d > %d", len(got), commitMsgMaxBytes)
	}
	if commitMsgMaxBytes-len(got) > 3 {
		t.Fatalf("UTF-8 边界回退过多（%d 字节）：%d", len(got), commitMsgMaxBytes-len(got))
	}
	if len(got) == commitMsgMaxBytes && !utf8.ValidString(long[:commitMsgMaxBytes]) {
		t.Fatal("切点合法却未截到上限，逻辑有误")
	}
}
