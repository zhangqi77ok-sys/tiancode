// 目录树用例：列出这场对话工作区内一层目录（右栏「目录」tab 的数据源）。
// 做什么：按会话根解析相对路径、校验后返回一层条目（目录在前、名称次序）。
// 被谁依赖：壳层（Wails 绑定 app/）。
// 依赖谁：stdlib（只读遍历；路径校验与 fstool 同强度）、platform/workspace（忽略清单）。
package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tiancode/internal/platform/workspace"
)

// DirEntry 是目录树的一层条目。ModTime 用 Unix 毫秒：JSON 里时间只剩数字，
// 排版交给前端（后端不掺展示格式）。
type DirEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	ModTime int64  `json:"modTime"`
}

// ListWorkspaceDir 列出这场对话工作区内 relPath 目录的下一层条目（右栏「目录」tab）。
//
// 根语义与 SearchWorkspaceFiles 同源：已落账会话用自己的归属根，草稿回退顶栏
// "下一场新对话"的根——树里点开的文件必须能被文件详情面板（ResolveSessionPath，
// 同一个根）读出来，两个视图共享同一基准才不会"树上点开、详情 404"。
//
// 路径校验强度对齐 fstool.resolve（比 ResolveSessionPath 强一档）：绝对路径拒绝；
// Clean 后对已存在的最深前缀做 EvalSymlinks——工作区内的符号链接/junction 可以
// 指到区外，纯词法前缀比对拦不住；再用解析后的真实路径做前缀判定。
// relPath 为空 = 根本身；无工作区 / 越界 / 缺失 / 非目录显式报错，绝不静默返回空列表。
func (s *ChatService) ListWorkspaceDir(sessionID, relPath string) ([]DirEntry, error) {
	root := s.sessionWorkspace(sessionID)
	if root == "" {
		root = s.Workspace() // 草稿：顶栏根就是这场对话即将归属的根
	}
	if root == "" {
		return nil, errors.New("当前没有工作区——目录树只在选择工作区后可用")
	}
	// 根与候选必须同一套真实路径解析（fstool.New 同款）：根保持符号链接原样时，
	// 候选解析成真实路径后前缀比对会把整个工作区误判成越界（CI 实测过的坑）
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	dir, err := resolveWithinRoot(root, relPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("目录不可用：%w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("不是目录：%s", relPath)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败：%w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir() // 目录在前
		}
		return entries[i].Name() < entries[j].Name()
	})
	out := make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		// 忽略目录过滤（0.0.21）：目录树与 search / @ 引用同一份清单（单一来源
		// platform/workspace）——0.0.12 登记了"filetree 承载忽略策略"但实际漏接，
		// node_modules/.git 一直原样出现在树里。0.0.26 起并入该工作区 .gitignore
		// 的顶层条目（out/ target/ 这类自定义输出目录）。
		if e.IsDir() && (workspace.IgnoredDir(e.Name()) || workspace.ExtraIgnoredDir(root, e.Name())) {
			continue
		}
		modTime := int64(0)
		if fi, ierr := e.Info(); ierr == nil {
			modTime = fi.ModTime().UnixMilli()
		}
		// 单个条目 stat 失败只丢修饰性时间戳，不中断整层列表——目录树以"列得出"
		// 为先（与 fstool.list 对 size 的降级同款纪律；目录项本身来自 ReadDir，可信）
		out = append(out, DirEntry{Name: e.Name(), IsDir: e.IsDir(), ModTime: modTime})
	}
	return out, nil
}

// resolveWithinRoot 把 root 内的相对路径解析成绝对路径（校验强度对齐 fstool.resolve）：
// 拒绝绝对路径；Clean 后做符号链接解析（目标不存在时对父目录解析——父目录在区内，
// 链接才能落到区外）；再用真实路径做前缀判定。
func resolveWithinRoot(root, relPath string) (string, error) {
	rel := strings.TrimSpace(relPath)
	if rel == "" {
		rel = "." // 空 = 根本身
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("不接受绝对路径：%s", rel)
	}
	clean := filepath.Clean(filepath.Join(root, rel))
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		clean = real
	} else if real, err := filepath.EvalSymlinks(filepath.Dir(clean)); err == nil {
		clean = filepath.Join(real, filepath.Base(clean))
	}
	if !withinDir(root, clean) {
		return "", fmt.Errorf("路径不在工作区内：%s", rel)
	}
	return clean, nil
}
