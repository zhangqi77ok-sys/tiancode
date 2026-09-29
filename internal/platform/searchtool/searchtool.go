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
	"runtime"
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
	// 符号链接/junction 解析（0.2.35 审计#4，与 fstool.resolve 同款）：
	// 工作区的链接可以指到区外，词法前缀检查拦不住。
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		clean = real
	} else if real, err := filepath.EvalSymlinks(filepath.Dir(clean)); err == nil {
		clean = filepath.Join(real, filepath.Base(clean))
	}
	if !t.hasRootPrefix(clean) {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}
	return clean, nil
}

// hasRootPrefix 判断路径是否在工作区内（0.2.36 审计 R4）：
// 两侧用同一套规范化（调用方保证），Windows 文件系统大小写不敏感——
// 前缀比较必须忽略大小写，否则 C:\Proj 与 c:\proj 会把整个工作区判成越界。
func (t *Tool) hasRootPrefix(p string) bool {
	if p == t.root {
		return true
	}
	root := t.root
	if runtime.GOOS == "windows" {
		p, root = strings.ToLower(p), strings.ToLower(root)
	}
	return strings.HasPrefix(p, root+string(filepath.Separator))
}

// insideRealRoot 判定文件路径的真实位置是否在工作区内（0.2.36 审计 R4）。
// resolved=false 表示符号链接解析失败（网络盘/暂锁文件）：调用方按词法路径
// 保留结果并在输出里标注——解析失败不能让正常文件从结果里消失。
func (t *Tool) insideRealRoot(p string) (inside bool, resolved bool) {
	real := p
	resolved = true
	if r, err := filepath.EvalSymlinks(p); err == nil {
		real = r
	} else if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		real = filepath.Join(r, filepath.Base(p))
	} else {
		resolved = false
	}
	return t.hasRootPrefix(real), resolved
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

	// 输出累加器（0.0.06 改头尾保留）：头部与尾部都在 64KiB 预算内，中间丢弃
	// 计数。为什么保尾：搜索命中流的后段（更深的目录/更晚的文件）与末行一样
	// 是真实结果，只留头部会谎报"后面的文件里没有"。
	acc := tools.NewHeadTailWriter(maxOutputBytes)
	outputTruncated := false
	matches := 0
	hitMax := false
	truncatedMatches := false
	// R4 汇总：确认落在区外的路径（跳过）与解析失败按词法保留的路径（标注）
	var skippedOutside, unresolvedLinks []string
	relSlash := func(p string) string {
		rel, err := filepath.Rel(t.root, p)
		if err != nil {
			return p
		}
		return filepath.ToSlash(rel)
	}
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
			// junction 目录：WalkDir 会走进去，但真实位置在区外时整棵跳过
			//（0.2.36 审计 R4；解析失败时保守继续——逐文件校验仍会兜底）
			if p != start {
				if inside, resolved := t.insideRealRoot(p); resolved && !inside {
					skippedOutside = append(skippedOutside, relSlash(p)+"/")
					return filepath.SkipDir
				}
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
		// 逐文件真实路径校验（0.2.36 审计 R4）：起点检查挡不住文件级符号链接；
		// 区外跳过并记入汇总；解析失败按词法保留但标注（不因一个链接整次失败）。
		if inside, resolved := t.insideRealRoot(p); !resolved {
			unresolvedLinks = append(unresolvedLinks, relSlash(p))
		} else if !inside {
			skippedOutside = append(skippedOutside, relSlash(p))
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
			acc.Write([]byte(line))
			if acc.Full() {
				// 预算耗尽：尾环已滚过一遍，此刻停下（尾部保留的是最后的命中）
				outputTruncated = true
				return errStop
			}
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

	content := strings.TrimRight(acc.String(), "\n")
	if outputTruncated {
		if content != "" {
			content += "\n"
		}
		content += "(truncated, output limit 64KiB)"
	}
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
	// R4 汇总（可观测性）：跳过与未解析都要写明，用户才能区分"没有匹配"
	// 与"有内容但被安全策略跳过"
	if len(skippedOutside) > 0 {
		content += fmt.Sprintf("\n(skipped %d path(s) resolving outside workspace: %s)",
			len(skippedOutside), strings.Join(headOf(skippedOutside, 5), ", "))
	}
	if len(unresolvedLinks) > 0 {
		content += fmt.Sprintf("\n(%d path(s) with unresolved symlinks, kept by lexical path: %s)",
			len(unresolvedLinks), strings.Join(headOf(unresolvedLinks, 5), ", "))
	}
	// 零命中且确实什么都没发生（无跳过、无未解析链接）才是真正的"no matches"。
	// 只搜到了被安全策略跳过的区外路径时不能谎报"没有匹配"——那会让模型和用户
	// 以为这段代码不存在（0.2.37 审计）；此时上面的 R4 汇总就是答案本身。
	if matches == 0 && content == "" && len(skippedOutside) == 0 && len(unresolvedLinks) == 0 {
		return tools.ToolResult{Content: "no matches"}, nil
	}
	if content == "" {
		content = "no matches"
	}
	return tools.ToolResult{Content: content}, nil
}

// headOf 取前 n 项（汇总行展示用，避免输出被路径列表撑爆）。
func headOf(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return items[:n]
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
