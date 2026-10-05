// Package codeintel 给模型提供编译级诊断与文件大纲（2026-10-05 设计）。
// 做什么：把"改动→知道改没改对"的反馈压到秒级——write 落盘后自动 vet 所在包，
// 错误内联回工具结果；symbols 输出带行号的符号骨架。
// 被谁依赖：internal/platform/fstool（write/replace 钩子与新 action）。
// 依赖谁：stdlib + go 工具链子进程（不 vendor x/tools、不依赖 gopls，
// 取舍见 docs/superpowers/specs/2026-10-05-codeintel-diagnostics-design.md）。
package codeintel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Diagnostic 是一条编译级诊断（路径已归一为工作区相对、正斜杠）。
type Diagnostic struct {
	File    string
	Line    int
	Col     int
	Message string
}

// 诊断子进程预算：auto（write 钩子）短、manual（fs.diagnose）宽。
// go build 缓存温热后单包秒级；冷启动大仓库超时即放弃——诊断是信息不是闸门。
const (
	AutoTimeout = 20 * time.Second
	ManualCap   = 25 * time.Second // 调用方 fs.Execute 的 30s 上限内留余量
	maxDiags    = 20
)

// Result 是一次诊断的产出：attempted=false 表示优雅跳过（note 说明原因）。
type Result struct {
	Diagnostics []Diagnostic
	Attempted   bool
	Note        string
}

// Diagnose 对工作区内一个 .go 文件所在的包做 go vet（含 _test.go）。
// root 是工作区绝对路径（模块根），relPath 是工作区相对路径。
// 非 .go / 无 go.mod / testdata、vendor 下 → 不执行并说明原因（调用方据此静默或提示）。
func Diagnose(ctx context.Context, root, relPath string, budget time.Duration) Result {
	if !strings.HasSuffix(strings.ToLower(relPath), ".go") {
		return Result{Note: "仅支持 Go 文件的编译诊断"}
	}
	if seg := strings.Split(filepath.ToSlash(relPath), "/"); containsAny(seg, "testdata", "vendor") {
		return Result{Note: "testdata/vendor 下的文件不参与编译诊断"}
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return Result{Note: "工作区没有 go.mod（不是 Go module），跳过编译诊断"}
	}
	pkgDir := filepath.Dir(filepath.FromSlash(relPath))
	if pkgDir == "." {
		pkgDir = "."
	} else {
		pkgDir = "./" + filepath.ToSlash(pkgDir)
	}
	runCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "go", "vet", pkgDir)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if runCtx.Err() != nil {
		if errors.Is(runCtx.Err(), context.Canceled) {
			return Result{Attempted: true, Note: "诊断已取消"}
		}
		return Result{Attempted: true, Note: fmt.Sprintf("诊断超时（%v，冷启动编译慢）", budget)}
	}
	if err != nil && !isVetFindings(err) && !looksLikeGoOutput(out) {
		// go 工具链本身的机制性失败（缺 go 命令等）：不伪装成代码诊断
		return Result{Attempted: true, Note: fmt.Sprintf("go vet 未能运行：%v", err)}
	}
	diags := parseVetOutput(string(out), root)
	if err == nil && len(diags) == 0 {
		return Result{Attempted: true} // 干净：无错误
	}
	return Result{Attempted: true, Diagnostics: diags}
}

// isVetFindings 区分"vet 报了问题"（预期）与"机制性失败"（go 缺失等）。
// vet 有发现时 exit 1；go 命令不存在时 exec 报 exec.ErrNotFound 系错误。
func isVetFindings(err error) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return true
	}
	return false
}

// looksLikeGoOutput 兜底：vet 对部分失败形态返回非 ExitError（如信号杀死），
// 但输出长得像诊断时按诊断处理（宁可展示可疑诊断，不静默吞掉）。
func looksLikeGoOutput(out []byte) bool {
	return strings.Contains(string(out), ": ") && strings.Count(string(out), ".go:") > 0
}

func containsAny(ss []string, want ...string) bool {
	for _, s := range ss {
		for _, w := range want {
			if s == w {
				return true
			}
		}
	}
	return false
}

// parseVetOutput 解析 go vet 输出里的 `path:line:col: msg` 行。
// `# package` 头与多行详情（tab 缩进）归并；路径绝对则转工作区相对。
func parseVetOutput(out, root string) []Diagnostic {
	var diags []Diagnostic
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "    ") {
			// 多行详情挂在上一条上（保持有界：只留首行详情）
			if len(diags) > 0 && len(diags[len(diags)-1].Message) < 200 {
				diags[len(diags)-1].Message += " " + strings.TrimSpace(line)
			}
			continue
		}
		d, ok := parseVetLine(line, root)
		if !ok {
			continue
		}
		diags = append(diags, d)
		if len(diags) >= maxDiags {
			break
		}
	}
	return diags
}

// parseVetLine 解析单行：file:line:col: msg（col 可缺省）。
// Windows 绝对路径（C:\x\y.go:9:2）会被 ":" 打散——按"首段为盘符"重组；
// 相对路径只认 file:line[:col] 两/三段形态，其余一律不认（宁可漏报不误报）。
func parseVetLine(line, root string) (Diagnostic, bool) {
	// go 工具链会把 exe 名放在行首（实测：`vet.exe: .\a.go:3:1: ...`）——
	// 不剥掉的话 "vet.exe" 会被当成文件路径、整行被拒（坏包诊断漏报）。
	if i := strings.Index(line, ".exe: "); i >= 0 && i < 40 {
		line = line[i+len(".exe: "):]
	}
	msgIdx := strings.Index(line, ": ")
	if msgIdx < 0 {
		return Diagnostic{}, false
	}
	loc, msg := line[:msgIdx], line[msgIdx+2:]
	parts := strings.Split(loc, ":")
	// 盘符重组：["C", "\proj\a.go", "9", "2"] → file=`C:\proj\a.go`，rest=["9","2"]
	if len(parts) >= 3 && len(parts[0]) == 1 && strings.HasPrefix(parts[1], `\`) {
		return buildDiag(parts[0]+":"+parts[1], parts[2:], root, msg)
	}
	if len(parts) < 2 || len(parts) > 3 {
		return Diagnostic{}, false
	}
	return buildDiag(parts[0], parts[1:], root, msg)
}

func buildDiag(file string, rest []string, root, msg string) (Diagnostic, bool) {
	lineNo := atoi(rest[0])
	if lineNo <= 0 {
		return Diagnostic{}, false
	}
	col := 0
	if len(rest) >= 2 {
		col = atoi(rest[1])
	}
	// `.\a.go` 形态 Clean 成 `a.go`（ToSlash 不去 ./ 前缀，可点解析会认生）
	file = filepath.ToSlash(filepath.Clean(file))
	if filepath.IsAbs(file) {
		if rel, err := filepath.Rel(root, file); err == nil && !strings.HasPrefix(rel, "..") {
			file = filepath.ToSlash(rel)
		}
	}
	return Diagnostic{
		File:    file,
		Line:    lineNo,
		Col:     col,
		Message: msg,
	}, true
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// Format 把诊断渲染为模型可读文本（path:line:col 可点形态，与 outputRows 同链路）。
// 超出 maxDiags 的在尾部计数标注；相对路径排序保证产出确定。
func Format(res Result) string {
	if !res.Attempted {
		return res.Note
	}
	if len(res.Diagnostics) == 0 {
		if res.Note != "" {
			return res.Note
		}
		return "编译诊断通过（go vet，含 _test.go）"
	}
	ds := make([]Diagnostic, len(res.Diagnostics))
	copy(ds, res.Diagnostics)
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].File != ds[j].File {
			return ds[i].File < ds[j].File
		}
		return ds[i].Line < ds[j].Line
	})
	var b strings.Builder
	fmt.Fprintf(&b, "[编译诊断] %d 处：", len(ds))
	for _, d := range ds {
		loc := fmt.Sprintf("%s:%d", d.File, d.Line)
		if d.Col > 0 {
			loc += fmt.Sprintf(":%d", d.Col)
		}
		fmt.Fprintf(&b, "\n%s: %s", loc, d.Message)
	}
	return b.String()
}
