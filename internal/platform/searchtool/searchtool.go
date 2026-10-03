// Package searchtool 实现工作区受控内容搜索。
//
// 做什么：按正则扫描工作区文本文件，返回 path:line:text 且每条命中带 ±2 行上下文
// （0.0.12）；files_only=true 时只按路径找文件、只返回路径列表。扫描两段式并行：
// 遍历收集候选（廉价判定），固定 worker pool 并行读内容，命中按路径排序后产出
// （输出确定性——与完成顺序无关）。跳过工作区默认忽略目录（单一来源
// internal/platform/workspace）与二进制。
// 被谁依赖：internal/app（装配进工具注册表）。
// 依赖谁：core/tools 端口、internal/platform/workspace、stdlib。
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
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"tiancode/internal/core/tools"
	"tiancode/internal/platform/workspace"
)

const (
	defaultTimeout    = 15 * time.Second
	maxFileBytes      = 1 << 20
	maxOutputBytes    = 64 * 1024
	defaultMaxMatches = 50
	capMaxMatches     = 200
	headProbe         = 8 * 1024
	// contextLines 是每条命中前后附带的行数（0.0.12，rg -C 同款）：只给命中那一行时，
	// 模型要改一个函数还得再 read 一次，每次定位多一轮往返。上下文计入 64KiB 输出
	// 预算——超了在块边界少给几条，绝不把整个函数贴进上下文。
	contextLines = 2
	// maxScanWorkers 是并行内容扫描的 worker 上限（runtime.NumCPU 封顶）：
	// 扫描是 IO+CPU 混合负载，超过核数收益递减，还要给前台留余量。
	maxScanWorkers = 8
)

// Tool 是工作区内容搜索工具。
type Tool struct {
	root    string
	timeout time.Duration
}

// New 使用默认 15s 超时。
func New(root string) *Tool { return NewWithTimeout(root, defaultTimeout) }

// NewWithTimeout 供测试注入短超时。
//
// root 与候选路径必须同一套真实路径解析（同 fstool.New：Windows 上
// EvalSymlinks 会把 8.3 短名解成长名，root 若保持短名，前缀比对会把
// 整个工作区误判成越界）。解析失败保持原值，行为与旧版一致。
func NewWithTimeout(root string, d time.Duration) *Tool {
	if d <= 0 {
		d = defaultTimeout
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	return &Tool{root: root, timeout: d}
}

// Name 实现工具端口。
func (t *Tool) Name() string { return "search" }

// Description 实现工具端口。
func (t *Tool) Description() string {
	return "在工作区搜索。默认搜内容（正则）：返回 path:line:text，每条命中带前后各 " +
		"2 行上下文（上下文行是 path-line-text，命中行是 path:line:text）。" +
		"files_only=true 时只按文件路径匹配、只返回路径列表（找 handler.go 这类「按名字找文件」用这个，" +
		"不要用 tree 逐层翻）。默认跳过 .git/.idea/.vscode/bin/build/dist/node_modules/obj/vendor。" +
		"不要用 shell 做全库 rg。"
}

// Schema 实现工具端口。
func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {"type": "string", "description": "Go 正则；files_only 时匹配工作区相对路径"},
    "path": {"type": "string", "description": "相对工作区的起点，默认 ."},
    "glob": {"type": "string", "description": "只匹配文件名，如 *.go"},
    "files_only": {"type": "boolean", "description": "只按路径找文件：不读文件内容，只返回路径列表（默认 false = 搜内容）"},
    "max_matches": {"type": "integer", "description": "命中上限，默认 50，最大 200"}
  },
  "required": ["pattern"]
}`)
}

// candidate 是遍历收集到的候选文件：abs 供 worker 打开读取，rel 是产出与排序
// 的统一键——喂给 worker 的顺序与最终产出顺序用同一把尺子，输出才与完成顺序无关。
type candidate struct {
	abs string
	rel string
}

// walkCandidates 单 goroutine 遍历收集候选文件（不读内容）：忽略目录整棵跳过
// （清单单一来源 internal/platform/workspace，与壳层 @ 引用同一份）、真实位置
// 在区外的 junction 整棵跳过、文件级符号链接逐个校验（0.2.36 审计 R4，跳过/
// 未解析都记入汇总）；contentMode 时套 1MiB 体积闸门（files_only 不读内容，
// 几 MB 的源文件也该按名字找到）。返回按工作区相对路径排序的候选。
func (t *Tool) walkCandidates(ctx context.Context, start, glob string, contentMode bool) (cands []candidate, skippedOutside, unresolvedLinks []string, err error) {
	walkErr := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 单个不可读条目不阻断遍历（尽力而为）
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			// 忽略判定 = 内置清单 ∪ 该工作区 .gitignore 的顶层条目（0.0.26）：
			// 用户自定义输出目录（out/ target/）此前会淹没搜索结果。
			if p != start && (workspace.IgnoredDir(d.Name()) || workspace.ExtraIgnoredDir(t.root, d.Name())) {
				return filepath.SkipDir
			}
			// junction 目录：WalkDir 会走进去，但真实位置在区外时整棵跳过
			//（0.2.36 审计 R4；解析失败时保守继续——逐文件校验仍会兜底）
			if p != start {
				if inside, resolved := t.insideRealRoot(p); resolved && !inside {
					skippedOutside = append(skippedOutside, t.relSlash(p)+"/")
					return filepath.SkipDir
				}
			}
			return nil
		}
		if glob != "" {
			if ok, _ := filepath.Match(glob, d.Name()); !ok {
				return nil
			}
		}
		if contentMode {
			info, err := d.Info()
			if err != nil || info.Size() > maxFileBytes {
				return nil
			}
		}
		// 逐文件真实路径校验（0.2.36 审计 R4）：起点检查挡不住文件级符号链接；
		// 区外跳过并记入汇总；解析失败按词法保留但标注（不因一个链接整次失败）。
		if inside, resolved := t.insideRealRoot(p); !resolved {
			unresolvedLinks = append(unresolvedLinks, t.relSlash(p))
		} else if !inside {
			skippedOutside = append(skippedOutside, t.relSlash(p))
			return nil
		}
		cands = append(cands, candidate{abs: p, rel: t.relSlash(p)})
		return nil
	})
	if walkErr != nil {
		return nil, skippedOutside, unresolvedLinks, walkErr
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].rel < cands[j].rel })
	return cands, skippedOutside, unresolvedLinks, nil
}

// relSlash 把绝对路径转成工作区相对的斜杠路径（产出串与排序键）；失败时原样返回。
func (t *Tool) relSlash(p string) string {
	rel, err := filepath.Rel(t.root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
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
		FilesOnly  bool   `json:"files_only"`
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
	var (
		matches          int
		truncatedMatches bool
		outputTruncated  bool
	)
	// 两段式：遍历只做廉价判定（忽略目录/越界/体积），读内容交给 worker pool。
	// R4 汇总在遍历期记好（skippedOutside/unresolvedLinks）。
	cands, skippedOutside, unresolvedLinks, walkErr := t.walkCandidates(ctx, start, a.Glob, !a.FilesOnly)
	if walkErr == nil {
		if a.FilesOnly {
			matches, truncatedMatches, outputTruncated = emitPathMatches(acc, cands, re, max)
		} else {
			matches, truncatedMatches, outputTruncated = emitContents(acc, t.scanContents(ctx, cands, re), max)
		}
	}

	content := strings.TrimRight(acc.String(), "\n")
	if outputTruncated {
		if content != "" {
			content += "\n"
		}
		content += "(truncated, output limit 64KiB)"
	}
	timedOut := ctx.Err() != nil
	if walkErr != nil && walkErr != context.DeadlineExceeded && walkErr != context.Canceled {
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

// scanContent 是一个文件的扫描产出（worker 与收集器之间的传递单元）。
type scanContent struct {
	rel   string
	hunks []outLine
}

// scanContents 固定 worker pool 并行扫内容（regexp.Regexp 并发安全）：候选已按
// 路径序投喂，worker 完成顺序天然乱序，收集后按路径排序——产出只由文件集合决定，
// 与调度无关。为什么不按命中配额早停：「前 N 条按路径排序」要求命中集合扫全，
// 半路收手会让产出集合依赖调度顺序；扫描总量由超时与取消兜底，结果只留有命中的
// 文件，常驻内存以「max+1 份单文件体积」为界。
func (t *Tool) scanContents(ctx context.Context, cands []candidate, re *regexp.Regexp) []scanContent {
	workers := runtime.NumCPU()
	if workers > maxScanWorkers {
		workers = maxScanWorkers
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan candidate)
	results := make(chan scanContent)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for c := range jobs {
				if ctx.Err() != nil {
					continue // 已取消：不再开新文件（在手的直接放弃，与超时收敛一致）
				}
				hunks, err := searchFile(ctx, c.abs, re)
				if err != nil || len(hunks) == 0 {
					continue // 读失败/二进制/无命中：逐文件跳过，与串行版一致
				}
				results <- scanContent{rel: c.rel, hunks: hunks}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, c := range cands {
			select {
			case jobs <- c:
			case <-ctx.Done():
				return
			}
		}
	}()
	var out []scanContent
	go func() {
		wg.Wait()
		close(results)
	}()
	for r := range results {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out
}

// emitPathMatches 产出 files_only 的路径列表：候选已按路径序，命中即一行路径
// （0.0.12：找 handler.go 不把任何文件内容拉进上下文）。
func emitPathMatches(acc *tools.HeadTailWriter, cands []candidate, re *regexp.Regexp, max int) (matches int, truncatedMatches, outputTruncated bool) {
	for _, c := range cands {
		if !re.MatchString(c.rel) {
			continue
		}
		if matches+1 > max {
			truncatedMatches = true
			return
		}
		if acc.Full() {
			outputTruncated = true
			return
		}
		acc.Write([]byte(c.rel + "\n"))
		matches++
	}
	return
}

// emitContents 把扫描结果按路径序产出：块 = 一段「命中 ± 上下文」的连续行
// （相邻命中自动合并）；配额放不下整块就整块不给——宁可少给几条，也不给半截
// 上下文。命中行 path:line:text（: 分隔即"这行是命中"），上下文行 path-line-text
// （rg 的 :/- 约定，一眼分辨）。
func emitContents(acc *tools.HeadTailWriter, results []scanContent, max int) (matches int, truncatedMatches, outputTruncated bool) {
	for _, r := range results {
		for i := 0; i < len(r.hunks); {
			end := i + 1
			for end < len(r.hunks) && !r.hunks[end].newHunk {
				end++
			}
			block := r.hunks[i:end]
			n := countHits(block)
			if matches+n > max {
				truncatedMatches = true
				return
			}
			if acc.Full() {
				// 预算耗尽：尾环已滚过一遍，此刻停下（尾部保留的是最后的命中）
				outputTruncated = true
				return
			}
			for _, l := range block {
				if l.match {
					acc.Write([]byte(fmt.Sprintf("%s:%d:%s\n", r.rel, l.line, l.text)))
				} else {
					acc.Write([]byte(fmt.Sprintf("%s-%d-%s\n", r.rel, l.line, l.text)))
				}
			}
			matches += n
			i = end
			if acc.Full() {
				outputTruncated = true
				return
			}
		}
	}
	return
}

// outLine 是一行输出：命中行（match）写成 path:line:text，上下文行写成
// path-line-text；newHunk 标记"新上下文块的开始"（块与块之间隔着被跳过的行），
// 输出预算按块边界检查，绝不把一块截成半截。
type outLine struct {
	line    int
	text    string
	match   bool
	newHunk bool
}

// countHits 数一块里有多少条命中（上下文行不占 max_matches 配额）。
func countHits(block []outLine) int {
	n := 0
	for _, l := range block {
		if l.match {
			n++
		}
	}
	return n
}

// searchFile 扫一遍文件：命中 + 前后各 contextLines 行上下文（相邻命中自动并块）。
// 整文件读进内存（调用方已按 maxFileBytes 1MiB 限制）——换来上下文/合并逻辑直白，
// 也省掉"先定位再回读"的第二遍 IO。
func searchFile(ctx context.Context, path string, re *regexp.Regexp) ([]outLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, headProbe)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, nil // 二进制：不是内容搜索的对象
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var (
		lines []string
		hits  []int // 0 起的行下标
	)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		text := strings.TrimRight(sc.Text(), "\r")
		lines = append(lines, text)
		if utf8.ValidString(text) && re.MatchString(text) {
			hits = append(hits, len(lines)-1)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return hunksOf(lines, hits), nil
}

// hunksOf 把命中展开成带上下文的输出行：±contextLines 的行都保留，相邻/重叠的窗口
// 自然并成一块（上下文不重复输出）。非法 UTF-8 行不出现在输出里（行号仍按真实行计）。
func hunksOf(lines []string, hits []int) []outLine {
	if len(hits) == 0 {
		return nil
	}
	emit := make([]bool, len(lines))
	isHit := make([]bool, len(lines))
	for _, hi := range hits {
		isHit[hi] = true
		lo := max(0, hi-contextLines)
		hi2 := min(len(lines)-1, hi+contextLines)
		for i := lo; i <= hi2; i++ {
			emit[i] = true
		}
	}
	out := make([]outLine, 0, len(hits)*(2*contextLines+1))
	newHunk := true
	for i, text := range lines {
		if !emit[i] {
			newHunk = true // 中间有被跳过的行：下一段是新块
			continue
		}
		if !utf8.ValidString(text) {
			continue // 二进制残留行：不输出（洞不并块，行号照真实行计）
		}
		out = append(out, outLine{line: i + 1, text: text, match: isHit[i], newHunk: newHunk})
		newHunk = false
	}
	return out
}
