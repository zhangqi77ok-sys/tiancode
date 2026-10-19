package searchtool

import (
	"bytes"
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// gitVisibleTimeout 是列出 git 可见文件的超时（大仓库 ls-files 也是秒级以内）。
const gitVisibleTimeout = 10 * time.Second

// gitVisible 返回工作区里 git 认为"不被忽略"的文件集合（已跟踪 + 未跟踪未忽略；
// 键为工作区相对的斜杠路径，Windows 上小写）。不是 git 仓库、没装 git 或执行失败
// 返回 nil——调用方退回旧行为（内置忽略清单 + 根 .gitignore 顶层目录）。
//
// 为什么（2026-10-19）：此前只认根 .gitignore 的顶层目录条目，子目录的 .gitignore、
// 文件级规则（*.log、生成的 *.pb.go）、.git/info/exclude 与全局排除全部不生效，
// 搜索结果被构建产物与日志淹没。交给 git 自己判定，规则与 git 完全一致。
func gitVisible(ctx context.Context, root string) map[string]bool {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, gitVisibleTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	hideConsole(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, p := range bytes.Split(out.Bytes(), []byte{0}) {
		if len(p) == 0 {
			continue
		}
		set[visibleKey(string(p))] = true
	}
	if len(set) == 0 {
		return nil // 空仓库/全被忽略：宁可退回旧行为，也不把搜索变成"永远零结果"
	}
	return set
}

// visibleKey 统一键形态（Windows 文件系统大小写不敏感）。
func visibleKey(rel string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(rel)
	}
	return rel
}
