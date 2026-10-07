// webdiag.go 前端/TS 工程的写后类型诊断（0.0.42，C-FS-8 的扩展面）：
// .ts/.tsx/.mts/.cts/.vue 落盘后跑工程级 vue-tsc/tsc --noEmit，把**本文件**的
// 诊断内联回工具结果。为什么只报本文件：工程级检查会带出其它文件的历史错误，
// 全文展开既淹没有效信息也超回执预算——其它文件的错误只给计数。
// 跳过（未装依赖/无 tsconfig/无检查器）一律 Attempted=false：自动钩子静默、
// 手动 fs.diagnose 明说（与 Go 侧同一纪律）。诊断是信息不是闸门。
package codeintel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// webExts 是参与类型诊断的扩展名。js/jsx 不进：无类型的 JS 工程跑 tsc 只会
// 产出海量噪音，等真实痛点（JSDoc checkJs 项目）出现再议。
var webExts = map[string]bool{
	".ts": true, ".tsx": true, ".mts": true, ".cts": true, ".vue": true,
}

// webProjBudget 是工程级类型检查的预算上限：vue-tsc 冷启动比 go vet 慢一个量级，
// 但仍受调用方 fs.Execute 的 30s 上限约束——超时即放弃并明说。
const webProjBudget = 25 * time.Second

// webProject 是定位好的前端工程：package.json+node_modules 所在目录与所用检查器。
type webProject struct {
	dir string
	// bin 是检查器可执行文件路径（Windows 下优先 .cmd shim）与展示名。
	bin string
	via string
	// tsconfig 是 -p 参数指向的配置文件相对路径（相对 dir）。
	tsconfig string
}

func isWebFile(relPath string) bool {
	return webExts[strings.ToLower(filepath.Ext(relPath))]
}

// locateWebProject 从文件所在目录向上（不超过 root）找最近的
// package.json+node_modules+tsconfig.json 组合；找不到返回 nil（调用方给跳过说明）。
func locateWebProject(root, relPath string) *webProject {
	dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(relPath)))
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	for {
		if webProjectAt(dir) {
			p := &webProject{dir: dir}
			if !p.pickChecker() || !p.pickTsconfig() {
				return nil
			}
			return p
		}
		if sameDir(dir, rootAbs) {
			return nil // 到工作区根仍不是工程：放弃（绝不越过根拿别人的 package.json）
		}
		parent := filepath.Dir(dir)
		if parent == dir || !strings.HasPrefix(strings.ToLower(dir), strings.ToLower(rootAbs)+string(os.PathSeparator)) {
			return nil
		}
		dir = parent
	}
}

func sameDir(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// webProjectAt 报告 dir 是否具备"已安装的前端工程"最低要件。
func webProjectAt(dir string) bool {
	if fi, err := os.Stat(filepath.Join(dir, "package.json")); err != nil || fi.IsDir() {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, "node_modules"))
	return err == nil && fi.IsDir()
}

// pickChecker 选类型检查器：有 vue-tsc 用 vue-tsc（认得 .vue 的 script 块），
// 否则 tsc；都没有 → false（记 Note）。
func (p *webProject) pickChecker() bool {
	for _, c := range []struct{ base, via string }{
		{"vue-tsc", "vue-tsc"},
		{"tsc", "tsc"},
	} {
		for _, name := range []string{c.base + ".cmd", c.base, c.base + ".exe"} {
			full := filepath.Join(p.dir, "node_modules", ".bin", name)
			if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
				p.bin, p.via = full, c.via
				return true
			}
		}
	}
	p.via = "未找到 vue-tsc/tsc（node_modules/.bin 下没有检查器）"
	return false
}

// pickTsconfig 找 -p 的配置：工程目录没有时向上一层找（monorepo 根配置），
// 超过工作区根放弃。vue-tsc/tsc 都支持 -p 指向任意路径。
func (p *webProject) pickTsconfig() bool {
	dir := p.dir
	rootAbs := p.dir
	for {
		if fi, err := os.Stat(filepath.Join(dir, "tsconfig.json")); err == nil && !fi.IsDir() {
			rel, err := filepath.Rel(p.dir, filepath.Join(dir, "tsconfig.json"))
			if err != nil {
				return false
			}
			p.tsconfig = filepath.ToSlash(rel)
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir || !strings.HasPrefix(strings.ToLower(dir), strings.ToLower(rootAbs)) {
			p.via = "未找到 tsconfig.json（纯 JS 工程不跑类型诊断）"
			return false
		}
		dir = parent
	}
}

// diagnoseWeb 跑工程级类型检查并过滤出本文件的诊断。
func diagnoseWeb(ctx context.Context, root, relPath string, budget time.Duration) Result {
	p := locateWebProject(root, relPath)
	if p == nil {
		return Result{Note: "不是已安装依赖的前端工程（缺 package.json/node_modules/tsconfig），跳过类型诊断"}
	}
	if budget <= 0 || budget > webProjBudget {
		budget = webProjBudget
	}
	runCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	cmd := exec.CommandContext(runCtx, p.bin, "--noEmit", "--pretty", "false", "-p", p.tsconfig)
	cmd.Dir = p.dir
	out, err := cmd.CombinedOutput()
	if runCtx.Err() != nil {
		if errors.Is(runCtx.Err(), context.Canceled) {
			return Result{Attempted: true, Note: "类型诊断已取消"}
		}
		return Result{Attempted: true, Via: p.via, Note: fmt.Sprintf("类型诊断超时（%v，vue-tsc/tsc 冷启动较慢）", budget)}
	}
	if err != nil && !isExitError(err) && !looksLikeWebOutput(out) {
		return Result{Attempted: true, Via: p.via, Note: fmt.Sprintf("%s 未能运行：%v", p.via, err)}
	}
	all := parseWebOutput(string(out), p.dir)
	if err != nil && len(all) == 0 {
		return Result{Attempted: true, Via: p.via, Inconclusive: true,
			Note: "退出异常且无可解析诊断：" + firstMeaningfulLine(string(out))}
	}
	// 过滤：只展开本文件的诊断；其它文件的只给计数。
	var mine []Diagnostic
	others := 0
	rel := filepath.ToSlash(filepath.Clean(relPath))
	for _, d := range all {
		if strings.EqualFold(d.File, rel) {
			mine = append(mine, d)
		} else {
			others++
		}
		if len(mine) >= maxDiags {
			break
		}
	}
	if len(mine) == 0 {
		if others > 0 {
			return Result{Attempted: true, Via: p.via, Note: fmt.Sprintf(
				"本文件无类型诊断；项目内其他文件有 %d 处（不随本次回执展开，可用 fs.diagnose 查看任意文件）", others)}
		}
		return Result{Attempted: true, Via: p.via} // 干净
	}
	return Result{Attempted: true, Via: p.via, Diagnostics: mine, Note: othersNote(others)}
}

func othersNote(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("（另有其他文件 %d 处诊断未展开）", n)
}

// parseWebOutput 解析 tsc/vue-tsc --pretty false 的单行诊断：
// `path(line,col): error TSxxxx: msg`（col 必有；路径相对工程目录，也可能绝对）。
func parseWebOutput(out, projDir string) []Diagnostic {
	var diags []Diagnostic
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" || strings.HasPrefix(line, "node_modules") {
			continue
		}
		d, ok := parseWebLine(line, projDir)
		if !ok {
			continue
		}
		diags = append(diags, d)
		if len(diags) >= maxDiags*4 { // 解析层多留些，过滤本文件后仍够
			break
		}
	}
	return diags
}

func parseWebLine(line, projDir string) (Diagnostic, bool) {
	open := strings.Index(line, "(")
	if open <= 0 {
		return Diagnostic{}, false
	}
	close := strings.Index(line[open:], "): ")
	if close < 0 {
		return Diagnostic{}, false
	}
	close += open
	file, loc, msg := line[:open], line[open+1:close], line[close+len("): "):]
	// loc = "line,col"
	comma := strings.Index(loc, ",")
	if comma < 0 {
		return Diagnostic{}, false
	}
	lineNo, col := atoi(loc[:comma]), atoi(loc[comma+1:])
	if lineNo <= 0 {
		return Diagnostic{}, false
	}
	file = filepath.ToSlash(filepath.Clean(file))
	if filepath.IsAbs(file) {
		if rel, err := filepath.Rel(projDir, file); err == nil && !strings.HasPrefix(rel, "..") {
			file = filepath.ToSlash(rel)
		}
	} else if strings.HasPrefix(file, "./") {
		file = strings.TrimPrefix(file, "./")
	}
	return Diagnostic{File: file, Line: lineNo, Col: col, Message: msg}, true
}

func isExitError(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}

func looksLikeWebOutput(out []byte) bool {
	return strings.Contains(string(out), "error TS") ||
		(strings.Contains(string(out), "(") && strings.Contains(string(out), "): "))
}

func firstMeaningfulLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return "无输出"
}
