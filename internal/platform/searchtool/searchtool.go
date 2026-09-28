// Package searchtool 实现工作区受控内容搜索。
//
// 做什么：按正则扫描工作区文本文件，返回 path:line:text；跳过内置忽略目录与二进制。
// 被谁依赖：internal/app（装配进工具注册表）。
// 依赖谁：core/tools 端口、stdlib。
package searchtool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"tiancode/internal/core/tools"
)

const (
	defaultTimeout    = 15 * time.Second
	maxFileBytes      = 1 << 20
	maxOutputBytes    = 64 * 1024
	defaultMaxMatches = 50
	capMaxMatches     = 200
	headProbe         = 8 * 1024
)

// Tool 是工作区内容搜索工具。
type Tool struct {
	root    string
	timeout time.Duration
}

// New 使用默认 15s 超时。
func New(root string) *Tool { return NewWithTimeout(root, defaultTimeout) }

// NewWithTimeout 供测试注入短超时。
func NewWithTimeout(root string, d time.Duration) *Tool {
	if d <= 0 {
		d = defaultTimeout
	}
	return &Tool{root: root, timeout: d}
}

// Name 实现工具端口。
func (t *Tool) Name() string { return "search" }

// Description 实现工具端口。
func (t *Tool) Description() string {
	return "在工作区内搜索文件内容（正则）。返回 path:line:text。默认跳过 .git/node_modules/vendor/dist/bin。不要用 shell 做全库 rg。"
}

// Schema 实现工具端口。
func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "Go 正则"},
    "path": {"type": "string", "description": "相对工作区的起点，默认 ."},
    "glob": {"type": "string", "description": "只匹配文件名，如 *.go"},
    "max_matches": {"type": "integer", "description": "命中上限，默认 50，最大 200"}
  },
  "required": ["pattern"]
}`)
}

func skipDirName(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "vendor", "dist", "bin":
		return true
	default:
		return false
	}
}

func (t *Tool) resolve(path string) (string, error) {
	if path == "" {
		path = "."
	}
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute path not allowed: %s", path)
	}
	clean := filepath.Clean(filepath.Join(t.root, path))
	if clean != t.root && !strings.HasPrefix(clean, t.root+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}
	return clean, nil
}

// Execute 实现工具端口。
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (res tools.ToolResult, err error) {
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{Content: "cancelled", IsError: true, TimedOut: true}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	var a struct {
		Pattern    string `json:"pattern"`
		Path       string `json:"path"`
		Glob       string `json:"glob"`
		MaxMatches int    `json:"max_matches"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return tools.ToolResult{Content: fmt.Sprintf("invalid arguments: %v", err), IsError: true}, nil
	}
	// 卡片语义标签：搜索词作主标签（defer 覆盖全部返回路径，含超时/无匹配）
	defer func() {
		if res.Title == "" {
			res.Op = "search"
			res.Title = tools.Headline(a.Pattern, 80)
		}
	}()
	if strings.TrimSpace(a.Pattern) == "" {
		return tools.ToolResult{Content: "pattern is required", IsError: true}, nil
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return tools.ToolResult{Content: fmt.Sprintf("invalid pattern: %v", err), IsError: true}, nil
	}
	if a.Glob != "" {
		if _, err := filepath.Match(a.Glob, "x"); err != nil {
			return tools.ToolResult{Content: fmt.Sprintf("invalid glob: %v", err), IsError: true}, nil
		}
	}
	max := a.MaxMatches
	if max < 1 {
		max = defaultMaxMatches
	}
	if max > capMaxMatches {
		max = capMaxMatches
	}
	start, err := t.resolve(a.Path)
	if err != nil {
		return tools.ToolResult{Content: err.Error(), IsError: true}, nil
	}

	var b strings.Builder
	matches := 0
	hitMax := false
	truncatedMatches := false
	walkErr := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != start && skipDirName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if a.Glob != "" {
			ok, _ := filepath.Match(a.Glob, d.Name())
			if !ok {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFileBytes {
			return nil
		}
		if hitMax {
			truncatedMatches = true
			return errStop
		}
		hits, err := searchFile(ctx, p, re)
		if err != nil && ctx.Err() == nil {
			return nil
		}
		rel, err := filepath.Rel(t.root, p)
		if err != nil {
			rel = p
		}
		rel = filepath.ToSlash(rel)
		for i, h := range hits {
			line := fmt.Sprintf("%s:%d:%s\n", rel, h.line, h.text)
			if b.Len()+len(line) > maxOutputBytes {
				b.WriteString("(truncated, output limit 64KiB)\n")
				return errStop
			}
			b.WriteString(line)
			matches++
			if matches >= max {
				if i+1 < len(hits) {
					truncatedMatches = true
					return errStop
				}
				hitMax = true
				break
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	})

	content := strings.TrimRight(b.String(), "\n")
	timedOut := ctx.Err() != nil
	if walkErr != nil && walkErr != errStop && walkErr != context.DeadlineExceeded && walkErr != context.Canceled {
		return tools.ToolResult{Content: fmt.Sprintf("search failed: %v", walkErr), IsError: true}, nil
	}
	if timedOut {
		if content == "" {
			return tools.ToolResult{Content: "TIMEOUT", IsError: true, TimedOut: true}, nil
		}
		return tools.ToolResult{Content: content + "\nTIMEOUT", TimedOut: true}, nil
	}
	if truncatedMatches {
		if content != "" {
			content += "\n"
		}
		content += fmt.Sprintf("(truncated, max_matches %d)", max)
	}
	if matches == 0 && content == "" {
		return tools.ToolResult{Content: "no matches"}, nil
	}
	return tools.ToolResult{Content: content}, nil
}

var errStop = fmt.Errorf("search stop")

type hit struct {
	line int
	text string
}

func searchFile(ctx context.Context, path string, re *regexp.Regexp) ([]hit, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, headProbe)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var hits []hit
	lineNo := 0
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return hits, err
		}
		lineNo++
		text := strings.TrimRight(sc.Text(), "\r")
		if !utf8.ValidString(text) {
			continue
		}
		if re.MatchString(text) {
			hits = append(hits, hit{line: lineNo, text: text})
		}
	}
	if err := ctx.Err(); err != nil {
		return hits, err
	}
	return hits, sc.Err()
}
