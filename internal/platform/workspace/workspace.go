// Package workspace 承载工作区目录扫描策略：默认忽略目录的单一来源。
//
// 为什么独立成包：忽略目录有两类消费方——适配器（search 扫内容/按名找文件）
// 与壳层（@ 引用列文件，经 internal/app 转出）。清单若归编排层，适配器够不着
// （platform 不许 import internal/app）；若归某个适配器，壳层策略就 rooted 在
// 可替换的实现层。平级策略包让两边真正同源，后续的用户级自定义也归这里扩展。
// 被谁依赖：internal/platform/searchtool、internal/app（转出给壳层 app/）。
// 依赖谁：仅 stdlib。
package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// defaultIgnoreDirs 是工作区默认忽略目录定稿清单（2026-10 定稿）：由两处硬编码
// 并集去重而来——searchtool 原有 .git/node_modules/vendor/dist/bin ∪ 壳层 @ 引用
// （SearchWorkspaceFiles）原有 .git/node_modules/vendor/dist/build/bin/obj/.idea/.vscode，
// 去重后恰好 9 项。自此两处都只引用 IgnoredDir，不再各留一份。
//
// 大小写不敏感：Windows/macOS 文件系统大小写不敏感，.GIT 就是 .git；Linux 上
// 宁可多跳过一个罕见的大写同名目录，也不把构建产物搜进结果（search 原本即此
// 规则，@ 引用从精确匹配收紧到同一规则只会少列噪音）。
//
// 这是默认值，不是用户协议：用户级自定义忽略（工作区设置里追加/删减）是后续项。
var defaultIgnoreDirs = map[string]bool{
	".git": true, ".idea": true, ".vscode": true,
	"bin": true, "build": true, "dist": true,
	"node_modules": true, "obj": true, "vendor": true,
}

// IgnoredDir 报告 name 是否工作区默认忽略目录名。只认单个目录名（不含分隔符），
// 调用方在遍历时对每层目录名判定；大小写不敏感。
func IgnoredDir(name string) bool {
	return defaultIgnoreDirs[strings.ToLower(name)]
}

// extraIgnore 是"这个工作区额外忽略的目录名"缓存（键 = 归一化后的根路径）。
// 为什么缓存：.gitignore 只在遍历开始时读一次（每个会话工具一份），中途改
// .gitignore 不立即生效是 git 自己的语义（要 git rm --cached 才生效）。
var extraIgnore sync.Map // map[string]map[string]bool

// GitignoreDirs 解析工作区根 .gitignore 的**顶层目录条目**，返回小写目录名集合
// （可与 IgnoredDir 取并集）。文件不存在/读不出 = 空集（不改变既有行为）。
//
// 范围刻意保守（0.0.26 审查"只解析顶层非 glob 条目"）：
//   - 只看根目录一个 .gitignore（子目录的 .gitignore 不递归解析——那会让
//     "在某子目录里忽略什么"影响全局搜索，语义上不对）；
//   - 跳过注释、空行、否定（!）、否定选择（^!）、glob（*?[）与含中间斜杠的
//     路径式条目（`build/asset`）——它们需要真正的 glob/pathspec 匹配，
//     半吊子实现会漏过滤或误过滤；
//   - 只收 `dir` 与 `dir/` 两种形态（用户自定义输出目录的典型写法）。
func GitignoreDirs(root string) []string {
	key := strings.ToLower(strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(root), `\`, "/"), "/"))
	if v, ok := extraIgnore.Load(key); ok {
		return sortedKeys(v.(map[string]bool))
	}
	set := parseGitignoreDirs(root)
	extraIgnore.Store(key, set)
	return sortedKeys(set)
}

// parseGitignoreDirs 读并解析 .gitignore（纯函数，可测；不碰缓存）。
func parseGitignoreDirs(root string) map[string]bool {
	set := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return set
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		if strings.ContainsAny(line, "*?[]^") {
			continue // glob/取反模式：不做半吊子匹配
		}
		if strings.Contains(line, "/") {
			dir := strings.TrimSuffix(line, "/")
			// 只接受顶层写法 `dir/`；含中间斜杠（build/asset）跳过
			if dir == "" || strings.Contains(dir, "/") {
				continue
			}
			line = dir
		}
		name := strings.ToLower(strings.TrimSpace(line))
		if name != "" && name != "." && name != ".." {
			set[name] = true
		}
	}
	return set
}

// ExtraIgnoredDir 报告 name 是否被该工作区的 .gitignore 顶层条目忽略（大小写不敏感）。
func ExtraIgnoredDir(root, name string) bool {
	key := strings.ToLower(strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(root), `\`, "/"), "/"))
	var set map[string]bool
	if v, ok := extraIgnore.Load(key); ok {
		set, _ = v.(map[string]bool)
	} else {
		set = parseGitignoreDirs(root)
		extraIgnore.Store(key, set)
	}
	return set[strings.ToLower(name)]
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
