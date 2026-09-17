package git

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	v1 "tiancode/pkg/plugin/v1"
)

// GitFileStatus 单文件状态
type GitFileStatus struct {
	Path       string `json:"path"`
	OrigPath   string `json:"orig_path,omitempty"`
	StagedCode string `json:"staged_code"`  // "M", "A", "D" 或 ""
	WorkCode   string `json:"work_code"`    // "M", "D", "?" 或 ""
}

// GitStatusReport 完整状态报表
type GitStatusReport struct {
	Branch       string          `json:"branch"`
	Staged       []GitFileStatus `json:"staged"`
	Working      []GitFileStatus `json:"working"`
	Untracked    []string        `json:"untracked"`
}

// Tool Git 物理受控算子插件
type Tool struct {
	id      string
	name    string
	version string
	rootDir string
}

// NewTool 构造实例
func NewTool(rootDir string) *Tool {
	return &Tool{
		id:      "tool.git",
		name:    "Git Source Control Tool",
		version: "1.0.0",
		rootDir: rootDir,
	}
}

func (t *Tool) ID() string             { return t.id }
func (t *Tool) Name() string           { return t.name }
func (t *Tool) Version() string        { return t.version }
func (t *Tool) Type() v1.PluginType    { return v1.TypeTool }
func (t *Tool) Init(ctx context.Context, cfg json.RawMessage) error { return nil }
func (t *Tool) Start(ctx context.Context) error { return nil }
func (t *Tool) Stop(ctx context.Context) error  { return nil }
func (t *Tool) Health(ctx context.Context) v1.HealthStatus {
	return v1.HealthStatus{Healthy: true, Message: "Git tool ready"}
}

func (t *Tool) Definition() v1.ToolDefinition {
	return v1.ToolDefinition{
		Name:        "git_control",
		Description: "查询工作区的物理 Git 暂存与分支状态",
		Mutating:    false,
	}
}

func (t *Tool) execGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = t.rootDir
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: 0x08000000,
			HideWindow:    true,
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s error: %v (stderr: %s)", strings.Join(args, " "), err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

// GetStatus 获取精准的双层暂存状态
func (t *Tool) GetStatus() (*GitStatusReport, error) {
	empty := &GitStatusReport{
		Branch:    "",
		Staged:    make([]GitFileStatus, 0),
		Working:   make([]GitFileStatus, 0),
		Untracked: make([]string, 0),
	}
	if _, err := os.Stat(filepath.Join(t.rootDir, ".git")); err != nil {
		return empty, nil
	}
	branch, _ := t.execGit("branch", "--show-current")
	rawStatus, err := t.execGit("status", "--porcelain=v2", "-uall")
	if err != nil {
		return empty, nil
	}
	return parsePorcelainV2(rawStatus, branch, t.rootDir), nil
}

// parsePorcelainV2 解析 git status --porcelain=v2 格式输出并防御带空格的文件路径与嵌入式空仓库
func parsePorcelainV2(rawStatus, branch string, rootDir ...string) *GitStatusReport {
	report := &GitStatusReport{
		Branch:    branch,
		Staged:    make([]GitFileStatus, 0),
		Working:   make([]GitFileStatus, 0),
		Untracked: make([]string, 0),
	}

	lines := strings.Split(rawStatus, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}

		switch parts[0] {
		case "1": // 普通更改: 1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path...>
			if len(parts) >= 9 {
				stagedCode := string(parts[1][0])
				workCode := string(parts[1][1])
				// 防御文件名含空格: 将索引 8 之后全部字段拼接恢复真实路径
				filePath := strings.Join(parts[8:], " ")

				// 若为子模块 (160000 / S...)，检测是否为无 HEAD 提交的损坏/空子仓库
				isSubmodule := (len(parts[2]) > 0 && parts[2][0] == 'S') || parts[4] == "160000" || parts[5] == "160000"
				if isSubmodule && len(rootDir) > 0 && rootDir[0] != "" {
					fullPath := filepath.Join(rootDir[0], filePath)
					if fi, err := os.Stat(fullPath); err == nil && fi.IsDir() {
						dotGit := filepath.Join(fullPath, ".git")
						if _, dotGitErr := os.Stat(dotGit); dotGitErr == nil {
							headCheck := exec.Command("git", "-C", fullPath, "rev-parse", "--verify", "HEAD")
							if headCheck.Run() != nil {
								// 没有有效 commit 的嵌套空仓库跳过
								continue
							}
						}
					}
				}

				if stagedCode != "." {
					report.Staged = append(report.Staged, GitFileStatus{
						Path:       filePath,
						StagedCode: stagedCode,
					})
				}
				if workCode != "." {
					report.Working = append(report.Working, GitFileStatus{
						Path:     filePath,
						WorkCode: workCode,
					})
				}
			}
		case "2": // 重命名文件: 2 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <X><score> <path><TAB><origPath>
			tabParts := strings.Split(line, "\t")
			fields := strings.Fields(tabParts[0])
			if len(fields) >= 10 {
				stagedCode := string(fields[1][0])
				workCode := string(fields[1][1])
				filePath := strings.Join(fields[9:], " ")
				origPath := ""
				if len(tabParts) >= 2 {
					origPath = strings.TrimSpace(tabParts[1])
				}

				if stagedCode != "." {
					report.Staged = append(report.Staged, GitFileStatus{
						Path:       filePath,
						OrigPath:   origPath,
						StagedCode: stagedCode,
					})
				}
				if workCode != "." {
					report.Working = append(report.Working, GitFileStatus{
						Path:     filePath,
						WorkCode: workCode,
					})
				}
			}
		case "?": // 未跟踪文件: ? <path...>
			if len(parts) >= 2 {
				filePath := strings.Join(parts[1:], " ")
				// 若以 / 结尾说明是目录/子模块候选
				if strings.HasSuffix(filePath, "/") && len(rootDir) > 0 && rootDir[0] != "" {
					cleanRel := strings.TrimSuffix(filePath, "/")
					fullPath := filepath.Join(rootDir[0], cleanRel)
					if fi, err := os.Stat(fullPath); err == nil && fi.IsDir() {
						dotGit := filepath.Join(fullPath, ".git")
						if _, dotGitErr := os.Stat(dotGit); dotGitErr == nil {
							headCheck := exec.Command("git", "-C", fullPath, "rev-parse", "--verify", "HEAD")
							if headCheck.Run() != nil {
								// 没有 commit 的嵌套空仓库无法被 stage，不暴露给单文件 diff
								continue
							}
						}
					}
				}
				report.Untracked = append(report.Untracked, filePath)
				report.Working = append(report.Working, GitFileStatus{
					Path:     filePath,
					WorkCode: "U",
				})
			}
		}
	}

	return report
}

// StageFile 暂存单个文件
func (t *Tool) StageFile(filePath string) error {
	trimmed := strings.TrimSpace(filePath)
	trimmed = strings.TrimRight(trimmed, "/\\")
	if trimmed == "" {
		return fmt.Errorf("empty file path")
	}

	cleanAbs := filepath.Join(t.rootDir, trimmed)
	if fi, err := os.Stat(cleanAbs); err == nil && fi.IsDir() {
		dotGit := filepath.Join(cleanAbs, ".git")
		if _, dotGitErr := os.Stat(dotGit); dotGitErr == nil {
			headCheck := exec.Command("git", "-C", cleanAbs, "rev-parse", "--verify", "HEAD")
			if errHead := headCheck.Run(); errHead != nil {
				return fmt.Errorf("nested repository '%s' has no commit checked out", trimmed)
			}
		}
	}

	out, err := t.execGit("add", "--", trimmed)
	if err != nil {
		if strings.Contains(out, "does not have a commit checked out") {
			return fmt.Errorf("nested repository '%s' has no commit checked out", trimmed)
		}
		return fmt.Errorf("git add failed: %w (output: %s)", err, out)
	}
	return nil
}

// UnstageFile 取消暂存单个文件 (优先 restore，降级 reset 与 rm --cached)
func (t *Tool) UnstageFile(filePath string) error {
	trimmed := strings.TrimSpace(filePath)
	trimmed = strings.TrimRight(trimmed, "/\\")
	if trimmed == "" {
		return fmt.Errorf("empty file path")
	}
	_, err := t.execGit("restore", "--staged", "--", trimmed)
	if err == nil {
		return nil
	}
	if _, resetErr := t.execGit("reset", "HEAD", "--", trimmed); resetErr == nil {
		return nil
	}
	_, rmErr := t.execGit("rm", "--cached", "-f", "--", trimmed)
	return rmErr
}

// RestoreFile 放弃工作区更改 (严格限制未追踪或无HEAD暂存文件才回退删除)
func (t *Tool) RestoreFile(filePath string) error {
	trimmed := strings.TrimSpace(filePath)
	trimmed = strings.TrimRight(trimmed, "/\\")
	if trimmed == "" {
		return fmt.Errorf("empty file path")
	}

	absPath := filepath.Join(t.rootDir, trimmed)
	cleanAbs, absErr := filepath.Abs(absPath)
	cleanRepo, repoErr := filepath.Abs(t.rootDir)
	if absErr != nil || repoErr != nil {
		return fmt.Errorf("invalid path resolution: %w", absErr)
	}

	volRepo := filepath.VolumeName(cleanRepo)
	normRepo := strings.ToUpper(volRepo) + cleanRepo[len(volRepo):]
	volAbs := filepath.VolumeName(cleanAbs)
	normAbs := strings.ToUpper(volAbs) + cleanAbs[len(volAbs):]
	rel, relErr := filepath.Rel(normRepo, normAbs)
	if relErr != nil || rel == "." || rel == "" || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
		strings.HasPrefix(rel, "../") {
		return fmt.Errorf("security violation: path escapes repo root: %s", filePath)
	}

	// 1. 查询当前文件的精确 Git 状态
	statusOut, _ := t.execGit("status", "--porcelain", "--", trimmed)
	statusStr := strings.TrimSpace(statusOut)

	// 若确认为未追踪文件 (??)，撤销即删除该未追踪新文件
	if strings.HasPrefix(statusStr, "??") {
		if fi, statErr := os.Stat(cleanAbs); statErr == nil && !fi.IsDir() {
			return os.Remove(cleanAbs)
		}
		return nil
	}

	// 针对无 HEAD 仓库（刚初始化尚未产生首次 commit）的暂存新增文件 (A / AM)
	// git restore 与 git checkout HEAD 均会因无法解析 HEAD 而失败 (exit status 128)
	// 此时安全解法：执行 git rm --cached -f 取消暂存并物理删除未提交的新文件
	_, errHead := t.execGit("rev-parse", "--verify", "HEAD")
	if errHead != nil && strings.HasPrefix(statusStr, "A") {
		_, _ = t.execGit("rm", "--cached", "-f", "--", trimmed)
		if fi, statErr := os.Stat(cleanAbs); statErr == nil && !fi.IsDir() {
			return os.Remove(cleanAbs)
		}
		return nil
	}

	// 2. 对于已追踪文件，优先使用 git restore --staged --worktree
	if _, err := t.execGit("restore", "--staged", "--worktree", "--", trimmed); err == nil {
		return nil
	}
	if _, err := t.execGit("restore", "--", trimmed); err == nil {
		return nil
	}
	// 降级使用 git checkout --
	_, checkoutErr := t.execGit("checkout", "--", trimmed)
	return checkoutErr
}

func (t *Tool) Execute(ctx context.Context, rawArgs json.RawMessage) (*v1.ToolResult, error) {
	status, err := t.GetStatus()
	if err != nil {
		return &v1.ToolResult{Content: fmt.Sprintf("git status error: %v", err), IsError: true}, nil
	}
	bytesOut, _ := json.Marshal(status)
	return &v1.ToolResult{Content: string(bytesOut), IsError: false}, nil
}
