// Package workspace 承载工作区目录扫描策略：默认忽略目录的单一来源。
//
// 为什么独立成包：忽略目录有两类消费方——适配器（search 扫内容/按名找文件）
// 与壳层（@ 引用列文件，经 internal/app 转出）。清单若归编排层，适配器够不着
// （platform 不许 import internal/app）；若归某个适配器，壳层策略就 rooted 在
// 可替换的实现层。平级策略包让两边真正同源，后续的用户级自定义也归这里扩展。
// 被谁依赖：internal/platform/searchtool、internal/app（转出给壳层 app/）。
// 依赖谁：仅 stdlib。
package workspace

import "strings"

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
