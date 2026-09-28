package tools

import "strings"

// Headline 截取卡片语义标题：超长截到 n 个 rune 并加省略号。
// 为什么在工具生产侧截而不是只在 UI 截：Title 会落账本并过 IPC，负载在源头可控；
// UI 仍可按容器宽度做视觉截断（truncate），两层职责不同。
func Headline(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
