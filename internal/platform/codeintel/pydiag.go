package codeintel

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Python 语法诊断（2026-10-19）：此前写后诊断只覆盖 Go 与 TS/Vue，Python 改坏了
// 要等跑起来才知道。只做语法级（ast.parse）：
//   - 不 import、不执行用户代码（py_compile/compileall 也不执行，但会往工作区写
//     __pycache__，这里连这个副作用都不要）；
//   - 类型/导入错误不在范围内（需要 pyright/mypy 与项目环境，诊断是信息不是闸门）。

// pySyntaxScript 读入文件做 ast.parse，语法错误打印成 path:line:col: msg 一行。
const pySyntaxScript = `import ast, sys
p = sys.argv[1]
try:
    ast.parse(open(p, 'rb').read(), p)
except SyntaxError as e:
    print("%s:%d:%d: %s: %s" % (p, e.lineno or 0, e.offset or 0, type(e).__name__, e.msg))
    sys.exit(1)
`

var pyDiagLine = regexp.MustCompile(`^(.*):(\d+):(\d+): (.*)$`)

func isPythonFile(rel string) bool {
	return strings.EqualFold(filepath.Ext(rel), ".py")
}

// diagnosePython 对单个 .py 文件做语法检查。找不到解释器 → 优雅跳过（Attempted=false）。
func diagnosePython(ctx context.Context, root, relPath string, budget time.Duration) Result {
	py := pythonExe()
	if py == "" {
		return Result{Note: "未找到 Python 解释器（python/py），跳过语法诊断"}
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, "-X", "utf8", "-c", pySyntaxScript, filepath.FromSlash(relPath))
	cmd.Dir = root
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return Result{Attempted: true, Inconclusive: true, Note: "Python 语法检查超时", Via: "python ast"}
	}
	if err == nil {
		return Result{Attempted: true, Via: "python ast"}
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return Result{Attempted: true, Inconclusive: true, Note: "Python 语法检查未能运行：" + err.Error(), Via: "python ast"}
	}
	var diags []Diagnostic
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		m := pyDiagLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		ln, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		diags = append(diags, Diagnostic{File: filepath.ToSlash(relPath), Line: ln, Col: col, Message: m[4]})
	}
	if len(diags) == 0 {
		return Result{Attempted: true, Inconclusive: true, Note: "Python 语法检查失败但没有可解析的输出：" + strings.TrimSpace(string(out)), Via: "python ast"}
	}
	return Result{Attempted: true, Diagnostics: diags, Via: "python ast"}
}

// pythonExe 选一个**真能运行**的解释器（进程内只探测一次）。
// 为什么要真跑一次：Windows 的 WindowsApps\python.exe 常是应用商店的别名桩，
// LookPath 找得到，但从程序里拉起直接 exit 9009、没有任何输出（本机实测）——
// 只看路径会把"没有 Python"误判成"语法检查失败"。py 启动器作为兜底。
func pythonExe() string {
	pythonOnce.Do(func() {
		for _, name := range []string{"python", "python3", "py"} {
			p, err := exec.LookPath(name)
			if err != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cmd := exec.CommandContext(ctx, p, "-c", "import sys; print(sys.version_info[0])")
			hideWindow(cmd)
			out, err := cmd.Output()
			cancel()
			if err == nil && strings.TrimSpace(string(out)) == "3" {
				pythonPath = p
				return
			}
		}
	})
	return pythonPath
}

var (
	pythonOnce sync.Once
	pythonPath string
)
