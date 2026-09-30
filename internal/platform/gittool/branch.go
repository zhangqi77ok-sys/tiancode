package gittool

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// CurrentBranch 返回 root 仓库的当前分支（0.0.11：界面顶栏显示，只读——绝不改仓库）。
//
// 语义（宁可"不显示"也不编造）：
//   - 普通分支（含还没有首个提交的"未出生"分支）：分支名；
//   - 游离 HEAD：短哈希（如 a1b2c3d）；
//   - 不是 git 仓库 / git 不可用 / 命令失败：空串，调用方据此不渲染分支。
func CurrentBranch(root string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	// symbolic-ref 在"未出生"分支上也有效（仓库还没有首个提交时 rev-parse 会失败，
	// 直接显示成"无分支"会让人以为仓库坏了）。
	if name, ok := gitOut(root, "symbolic-ref", "--short", "HEAD"); ok {
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}
	// 游离 HEAD：symbolic-ref 报错，退到短哈希（拿不到就什么都不显示）
	if hash, ok := gitOut(root, "rev-parse", "--short", "HEAD"); ok {
		if hash = strings.TrimSpace(hash); hash != "" {
			return hash
		}
	}
	return ""
}

// gitOut 跑一次只读 git 命令并取 stdout：失败/超时/目录不存在一律 ok=false
// （调用方按"没有这条信息"处理，不把它当错误上报——顶栏不该因此弹错误框）。
func gitOut(root string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	hideConsole(cmd)
	cmd.Dir = root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return buf.String(), true
}
