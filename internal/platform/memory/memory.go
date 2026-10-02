// Package memory 实现跨对话的长期记忆（两级：全局 + 工作区）。
//
// 做什么：把"用户偏好 / 项目约定"存成 Markdown 文件（模型经 memory 工具读写，
// 每轮对话注入系统提示），让模型在下一次对话里记得车主的习惯与项目的规矩。
// 两级 scope：global（全局偏好，如"回复用中文"）随应用走；workspace（项目约定，
// 如"提交说明用 conventional commits"）按工作区各存一份。
//
// 存储形态：纯 Markdown 行（每行一条），放 configfile.Dir()/memory/ 下。
// 为什么不用 JSON：这份内容模型要整读整写，Markdown 对人和模型都可读可 diff；
// 每行一条让"删除第 N 条"有稳定锚点。写盘走原子写（配置半截最难受）。
// 被谁依赖：internal/app（装配为共享工具 + 每轮系统说明注入）。
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"tiancode/internal/platform/atomicfile"
	"tiancode/internal/platform/configfile"
)

// ErrNoWorkspace 表示"这场对话没有工作区"（workspace scope 因此不可用）。
// 纯对话是一等模式：注入路径把它当"没有项目记忆"，工具路径把它作为
// 业务失败反馈给模型——调用方用 errors.Is 区分，绝不靠字符串匹配。
var ErrNoWorkspace = errors.New("这场对话没有工作区，没有项目记忆可用")

// maxFileBytes 是单个记忆文件的字节上限：记忆进系统提示，无上限会悄悄吃光
// 上下文预算（有界纪律对齐 webfetch 的响应体上限）。超限追加显式报错，
// 让模型先清理再记，而不是静默丢内容。
const maxFileBytes = 32 << 10

// maxLines 是单文件行数上限：行数过多注入也会挤占上下文（双重硬顶）。
const maxLines = 200

// Scope 是记忆的作用域：global 跟应用走，workspace 按工作区各存一份。
type Scope string

const (
	ScopeGlobal    Scope = "global"
	ScopeWorkspace Scope = "workspace"
)

// Store 是两级记忆存储。并发安全（模型工具与每轮注入会并发读写）。
type Store struct {
	mu  sync.Mutex
	dir string
}

// NewStore 构造存储；dir 为空 = 缺省 %APPDATA%\tiancode\memory。
// 目录在首次写入时惰性创建（只读路径绝不写盘）。
func NewStore(dir string) *Store {
	if strings.TrimSpace(dir) == "" {
		dir = filepath.Join(configfile.Dir(), "memory")
	}
	return &Store{dir: dir}
}

// workspaceKey 统一工作区 key 形态（与 app 层 workspace-settings 的 key 同规则）：
// 绝对路径 → 正斜杠 → 去尾斜杠 → 小写（Windows 大小写不敏感，同一目录的两种
// 写法必须落到同一份记忆）。这里独立实现：platform 层不能反向依赖 app 包。
func workspaceKey(root string) string {
	clean := strings.ReplaceAll(strings.TrimSpace(root), `\`, "/")
	clean = strings.TrimRight(clean, "/")
	return strings.ToLower(clean)
}

// workspaceFile 把工作区 key 映射成文件名：sha256 前 16 hex。路径里的大小写、
// 盘符、非法字符都不进文件名，两个项目永不撞文件。
func workspaceFile(root string) string {
	sum := sha256.Sum256([]byte(workspaceKey(root)))
	return "ws-" + hex.EncodeToString(sum[:8]) + ".md"
}

// pathOf 返回 scope 对应的文件路径；workspace scope 必须带非空 root。
func (s *Store) pathOf(scope Scope, root string) (string, error) {
	switch scope {
	case ScopeGlobal:
		return filepath.Join(s.dir, "global.md"), nil
	case ScopeWorkspace:
		root = strings.TrimSpace(root)
		if root == "" {
			return "", ErrNoWorkspace
		}
		return filepath.Join(s.dir, workspaceFile(root)), nil
	default:
		return "", fmt.Errorf("未知的记忆作用域：%q", scope)
	}
}

// readLines 读某 scope 的全部行（不含空行；文件缺失 = 空切片，不是错误——
// "还没有记忆"是常态，不该让工具报错或注入段凭空出现）。
func (s *Store) readLines(scope Scope, root string) ([]string, error) {
	p, err := s.pathOf(scope, root)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取记忆失败：%w", err)
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// writeLines 原子写回（行间 \n，尾带换行）；行数超上限拒绝。
func (s *Store) writeLines(scope Scope, root string, lines []string) error {
	if len(lines) > maxLines {
		return fmt.Errorf("记忆已满（%d 条上限），请先删除旧条目再追加", maxLines)
	}
	p, err := s.pathOf(scope, root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("创建记忆目录失败：%w", err)
	}
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if len(content) > maxFileBytes {
		return fmt.Errorf("记忆超出 %d KB 上限，请先精简再追加", maxFileBytes/1024)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := atomicfile.WriteFileAtomic(p, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入记忆失败：%w", err)
	}
	return nil
}

// Append 追加一条记忆（多行文本按行拆开，每行一条；空行丢弃）。
// 单文件总量超限显式报错。
func (s *Store) Append(scope Scope, root, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("记忆内容为空")
	}
	lines, err := s.readLines(scope, root)
	if err != nil {
		return err
	}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		lines = append(lines, l)
	}
	return s.writeLines(scope, root, lines)
}

// Delete 删除第 line 条（1 基，对模型更直观）。越界显式报错（绝不静默删错行）。
func (s *Store) Delete(scope Scope, root string, line int) error {
	if line < 1 {
		return fmt.Errorf("行号从 1 开始：%d", line)
	}
	lines, err := s.readLines(scope, root)
	if err != nil {
		return err
	}
	if line > len(lines) {
		return fmt.Errorf("记忆共 %d 条，没有第 %d 条", len(lines), line)
	}
	lines = append(lines[:line-1], lines[line:]...)
	return s.writeLines(scope, root, lines)
}

// Lines 返回某 scope 的记忆行（只读展示用）。
func (s *Store) Lines(scope Scope, root string) ([]string, error) {
	return s.readLines(scope, root)
}
