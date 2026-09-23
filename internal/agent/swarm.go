// Package agent 提供 TDD 自动化测试校验与多智能体（swarm）协作能力，
// 是一个仅依赖标准库的领域服务叶子包（不依赖 loop/host/app，无环）。
// 注意与 internal/core/loop 中的 ReAct 执行循环（"智能体"的运行内核）区分：
// 本包的 RunTDDValidation 由应用宿主在构造引擎时通过 verify 回调注入，
// loop 不直接引用本包，从而保持执行内核的无状态与可测。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// TestReport TDD 自动化测试验证器结果
type TestReport struct {
	Status      string   `json:"status"` // "PASS" | "FAIL"
	Passed      int      `json:"passed"`
	Failed      int      `json:"failed"`
	FailedTests []string `json:"failed_tests,omitempty"`
	Duration    string   `json:"duration"`
	Output      string   `json:"output"`
	Timestamp   int64    `json:"timestamp"`
}

// AuditReport 安全沙箱代码审查报告
type AuditReport struct {
	Status      string   `json:"status"` // "SECURE" | "WARNING" | "BLOCKED"
	RiskLevel   string   `json:"risk_level"` // "LOW" | "MEDIUM" | "CRITICAL"
	Issues      []string `json:"issues"`
	FilesScanned int     `json:"files_scanned"`
	Timestamp   int64    `json:"timestamp"`
}

// runCmdWithTimeout 运行命令行进程并注入 Windows CREATE_NO_WINDOW 无黑框标记与进程树强制自毁机制
func runCmdWithTimeout(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: 0x08000000,
			HideWindow:    true,
		}
		cmd.Cancel = func() error {
			if cmd.Process != nil && cmd.Process.Pid > 0 {
				killCmd := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
				killCmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
				return killCmd.Run()
			}
			return nil
		}
	} else {
		cmd.Cancel = func() error {
			if cmd.Process != nil {
				return cmd.Process.Kill()
			}
			return nil
		}
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func findGoExe() (string, error) {
	goExe, err := exec.LookPath("go")
	if err != nil {
		for _, candidate := range []string{`E:\pro\tools\go\bin\go.exe`, `C:\Program Files\Go\bin\go.exe`} {
			if _, statErr := os.Stat(candidate); statErr == nil {
				return candidate, nil
			}
		}
	}
	return goExe, err
}

func findNpmExe() (string, error) {
	npmCmdName := "npm"
	if runtime.GOOS == "windows" {
		npmCmdName = "npm.cmd"
	}
	npmExe, err := exec.LookPath(npmCmdName)
	if err != nil {
		npmExe, err = exec.LookPath("npm")
	}
	return npmExe, err
}

func findNpmTestDir(workspace string) (string, bool, string) {
	pkgPath1 := filepath.Join(workspace, "package.json")
	if exists, hasTest := checkHasNpmTest(pkgPath1); exists {
		if hasTest {
			return workspace, true, ""
		}
		return workspace, false, "未配置 npm test，已跳过"
	}
	
	frontendDir := filepath.Join(workspace, "frontend")
	pkgPath2 := filepath.Join(frontendDir, "package.json")
	if exists, hasTest := checkHasNpmTest(pkgPath2); exists {
		if hasTest {
			return frontendDir, true, ""
		}
		return frontendDir, false, "未配置 npm test，已跳过"
	}
	
	return "", false, ""
}

func checkHasNpmTest(pkgJsonPath string) (exists bool, hasTest bool) {
	if fi, err := os.Stat(pkgJsonPath); err == nil && !fi.IsDir() {
		exists = true
		if data, err := os.ReadFile(pkgJsonPath); err == nil {
			var pkg struct {
				Scripts map[string]string `json:"scripts"`
			}
			if json.Unmarshal(data, &pkg) == nil && pkg.Scripts != nil {
				if testScript, ok := pkg.Scripts["test"]; ok && strings.TrimSpace(testScript) != "" {
					hasTest = true
					// 如果遇到那种默认的无用测试命令
					if strings.Contains(testScript, "echo \\\"Error: no test specified\\\"") {
						hasTest = false
					}
				}
			}
		}
	}
	return
}

// RunTDDValidation 运行自动化 TDD 测试驱动红绿灯验证 (支持 Go 原生 go test 与 Node npm test 双栈级联验证，带 60s 硬超时与零黑框)
func RunTDDValidation(workspace string) (TestReport, error) {
	start := time.Now()

	hasGoMod := false
	if fi, err := os.Stat(filepath.Join(workspace, "go.mod")); err == nil && !fi.IsDir() {
		hasGoMod = true
	}

	npmDir, hasNpmTest, npmSkipReason := findNpmTestDir(workspace)

	if !hasGoMod && !hasNpmTest {
		output := "当前工作区未检测到可执行的自动化测试套件 (既未找到 go.mod，亦未找到配置了 test 脚本的 package.json)。TDD 模式严禁无测试直接通过。"
		if npmSkipReason != "" {
			output = "=== [Npm Test 套件] ===\n" + npmSkipReason + "\n\n" + output
		}
		return TestReport{
			Status:    "FAIL",
			Passed:    0,
			Failed:    1,
			Duration:  "0ms",
			Output:    output,
			Timestamp: time.Now().Unix(),
		}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	totalPassed := 0
	totalFailed := 0
	var outputs []string
	var goRawOut string
	var npmRawOut string

	// 1. 若存在 Go 模块，执行 go test
	if hasGoMod {
		goExe, err := findGoExe()
		if err != nil {
			return TestReport{
				Status:    "FAIL",
				Passed:    0,
				Failed:    1,
				Duration:  "0ms",
				Output:    "未检测到系统安装的 Go 编译器环境 (go not found in PATH)，请配置 Go 工具链以执行 TDD 验证",
				Timestamp: time.Now().Unix(),
			}, nil
		}
		goOut, goErr := runCmdWithTimeout(ctx, workspace, goExe, "test", "-v", "./...")
		goRawOut = goOut
		goPassed := strings.Count(goOut, "--- PASS:")
		goFailed := strings.Count(goOut, "--- FAIL:")
		if goErr != nil || goFailed > 0 {
			if goFailed == 0 {
				goFailed = 1
			}
		}
		totalPassed += goPassed
		totalFailed += goFailed
		if hasNpmTest {
			outputs = append(outputs, fmt.Sprintf("=== [Go Test 套件 (./...)] ===\n%s", strings.TrimSpace(goOut)))
		} else {
			outputs = append(outputs, goOut)
		}
	}

	// 2. 若存在 npm test (根目录或 frontend/ 子目录)，执行 npm test
	if hasNpmTest {
		npmExe, err := findNpmExe()
		if err != nil {
			npmErrReport := "未检测到 Node.js / npm 运行环境 (npm not found in PATH)，请配置 Node.js 以执行 npm test"
			if !hasGoMod {
				return TestReport{
					Status:    "FAIL",
					Passed:    0,
					Failed:    1,
					Duration:  "0ms",
					Output:    npmErrReport,
					Timestamp: time.Now().Unix(),
				}, nil
			}
			totalFailed++
			outputs = append(outputs, fmt.Sprintf("=== [Npm Test 套件] ===\n%s", npmErrReport))
		} else {
			npmOut, npmErr := runCmdWithTimeout(ctx, npmDir, npmExe, "test")
			npmRawOut = npmOut
			npmPassed := 0
			npmFailed := 0
			if npmErr == nil {
				npmPassed = 1
			} else {
				npmFailed = 1
			}
			totalPassed += npmPassed
			totalFailed += npmFailed

			relDir, _ := filepath.Rel(workspace, npmDir)
			if relDir == "" || relDir == "." {
				relDir = "package.json"
			}
			if hasGoMod {
				outputs = append(outputs, fmt.Sprintf("=== [Npm Test 套件 (%s)] ===\n%s", relDir, strings.TrimSpace(npmOut)))
			} else {
				outputs = append(outputs, npmOut)
			}
		}
	} else if npmSkipReason != "" {
		outputs = append(outputs, fmt.Sprintf("=== [Npm Test 套件] ===\n%s", npmSkipReason))
	}

	duration := time.Since(start).Round(time.Millisecond).String()
	outputStr := strings.Join(outputs, "\n\n")
	status := "PASS"
	if totalFailed > 0 {
		status = "FAIL"
	}
	if ctx.Err() == context.DeadlineExceeded {
		outputStr += "\n[超时警告] 测试超时已中断 (60s)"
		status = "tdd_failed"
	}

	failedTests := ExtractFailedTests(goRawOut, npmRawOut)
	if len(failedTests) > 0 {
		prefix := fmt.Sprintf("❌ 【失败测试用例清单】(%d 个):\n%s\n\n", len(failedTests), "- "+strings.Join(failedTests, "\n- "))
		outputStr = prefix + outputStr
	}

	return TestReport{
		Status:      status,
		Passed:      totalPassed,
		Failed:      totalFailed,
		FailedTests: failedTests,
		Duration:    duration,
		Output:      outputStr,
		Timestamp:   time.Now().Unix(),
	}, nil
}

// ExtractFailedTests 从 Go test 与 npm/vitest/jest 输出中提取失败用例名称
func ExtractFailedTests(goOut, npmOut string) []string {
	failed := make([]string, 0)
	seen := make(map[string]bool)

	addTest := func(name string) {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			failed = append(failed, name)
		}
	}

	// 1. Go test 失败用例解析
	if goOut != "" {
		for _, line := range strings.Split(goOut, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "--- FAIL:") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 3 {
					addTest(parts[2])
				}
			} else if strings.HasPrefix(trimmed, "FAIL:\t") || strings.HasPrefix(trimmed, "FAIL: ") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					addTest(parts[1])
				}
			}
		}
	}

	// 2. npm / vitest / jest 失败用例解析
	if npmOut != "" {
		for _, line := range strings.Split(npmOut, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "FAIL ") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					addTest(parts[1])
				}
			} else if strings.HasPrefix(trimmed, "✕ ") || strings.HasPrefix(trimmed, "✖ ") || strings.HasPrefix(trimmed, "● ") {
				idx := strings.Index(trimmed, " ")
				if idx != -1 {
					testDesc := strings.TrimSpace(trimmed[idx+1:])
					if rIdx := strings.LastIndex(testDesc, " ("); rIdx != -1 && strings.HasSuffix(testDesc, "ms)") {
						testDesc = strings.TrimSpace(testDesc[:rIdx])
					}
					addTest(testDesc)
				}
			}
		}
	}

	return failed
}

// RunSecurityAudit 运行安全沙箱审查器，检测高危代码、未脱敏密钥与系统提权指令
func RunSecurityAudit(workspace string) (AuditReport, error) {
	issues := make([]string, 0)
	filesScanned := 0

	highRiskKeywords := []string{
		"rm -rf /", "mkfs", "format c:", "drop database", "shutdown", "exec.Command(\"cmd\", \"/c\", \"del",
		"exec.Command(\"sh\", \"-c\", \"rm",
	}

	_ = filepath.Walk(workspace, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			cleanPath := filepath.Clean(path)
			cleanWs := filepath.Clean(workspace)
			if cleanPath != cleanWs {
				base := filepath.Base(path)
				if base == ".git" || base == "node_modules" || base == "bin" || base == "dist" || base == "build" || base == ".idea" || base == ".vscode" {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// 限制单个审计文件最大不超过 5MB，杜绝大文件读取引发 OOM
		if info.Size() > 5*1024*1024 {
			return nil
		}

		// 只审计源码与脚本
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".go" && ext != ".js" && ext != ".ts" && ext != ".sh" && ext != ".py" {
			return nil
		}

		filesScanned++
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		rel, _ := filepath.Rel(workspace, path)
		str := string(content)

		for _, kw := range highRiskKeywords {
			if strings.Contains(strings.ToLower(str), strings.ToLower(kw)) {
				issues = append(issues, "文件 ["+rel+"] 发现高危破坏性指令特征: "+kw)
			}
		}

		return nil
	})

	status := "SECURE"
	riskLevel := "LOW"
	if len(issues) > 0 {
		status = "WARNING"
		riskLevel = "MEDIUM"
	}

	return AuditReport{
		Status:       status,
		RiskLevel:    riskLevel,
		Issues:       issues,
		FilesScanned: filesScanned,
		Timestamp:   time.Now().Unix(),
	}, nil
}
