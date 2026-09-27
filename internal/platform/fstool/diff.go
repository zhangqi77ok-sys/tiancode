package fstool

import (
	"fmt"
	"strings"
)

// maxDiffLines 限制 diff 行数。
// 为什么有上限：工具卡片用于"快速确认改了什么"，完整审计由账本承担；
// 不设上限会让一次大文件写入撑爆事件流与 UI。
const maxDiffLines = 200

// diffContext 是变更处上下保留的行数（够看清改动位置，又不至于把文件全贴出来）。
const diffContext = 3

// diffText 生成简化的逐行 diff（空串表示无变化）。
//
// 为什么不用 LCS/Myers 完整算法：文件编辑的实际形态是"改几行、插几行"，
// 用"公共前缀 + 公共后缀裁剪 + 中段整体替换"表达足以看清改动，
// 且实现无组合爆炸风险（完整 diff 库属于过度设计，见 ADR-0006）。
func diffText(path, oldText, newText string) string {
	if oldText == newText {
		return ""
	}
	oldLines := strings.Split(strings.TrimSuffix(oldText, "\n"), "\n")
	newLines := strings.Split(strings.TrimSuffix(newText, "\n"), "\n")
	if oldText == "" {
		oldLines = nil
	}
	if newText == "" {
		newLines = nil
	}

	// 公共前缀 / 后缀（按行）
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}

	oldMid := oldLines[prefix : len(oldLines)-suffix]
	newMid := newLines[prefix : len(newLines)-suffix]

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n", path)
	fmt.Fprintf(&b, "+++ %s\n", path)
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", prefix+1, len(oldMid), prefix+1, len(newMid))

	// 前置上下文
	ctxStart := prefix - diffContext
	if ctxStart < 0 {
		ctxStart = 0
	}
	for _, line := range oldLines[ctxStart:prefix] {
		fmt.Fprintf(&b, " %s\n", line)
	}
	for _, line := range oldMid {
		fmt.Fprintf(&b, "-%s\n", line)
	}
	for _, line := range newMid {
		fmt.Fprintf(&b, "+%s\n", line)
	}
	// 后置上下文
	ctxEnd := len(oldLines) - suffix + diffContext
	if ctxEnd > len(oldLines) {
		ctxEnd = len(oldLines)
	}
	for _, line := range oldLines[len(oldLines)-suffix : ctxEnd] {
		fmt.Fprintf(&b, " %s\n", line)
	}

	return truncateDiff(b.String())
}

// truncateDiff 截断超长 diff 并显式标注（静默截断会让人以为"只改了这些"）。
func truncateDiff(d string) string {
	lines := strings.Split(d, "\n")
	if len(lines) <= maxDiffLines {
		return d
	}
	head := strings.Join(lines[:maxDiffLines], "\n")
	return fmt.Sprintf("%s\n…（diff 已截断，共 %d 行，完整内容见会话账本）\n", head, len(lines)-1)
}
