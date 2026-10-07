// Package gittool 实现只读 git 工具（status / diff / log）。
// 做什么：让模型查看仓库状态与变更，作为"跑测试→读输出→修文件"闭环的观察手段。
// 被谁依赖：internal/app（装配进工具注册表）。
// 依赖谁：core/tools 端口、stdlib（调用系统 git 可执行文件）。
//
// 为什么只读：add/commit/push 是不可逆的仓库状态变更，MVP 不纳入；
// 需要时按 ADR-0005 准入判据复评后再加（且届时必须走审批链，见 M5 之后的路线）。
package gittool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"tiancode/internal/core/tools"
)

// gitTimeout 是单次 git 命令超时（只读操作毫秒级，30s 防御病态仓库）。
const gitTimeout = 30 * time.Second

// outputLimit 是输出上限（git diff 可能极大）。
const outputLimit = 64 * 1024

// maxUntrackedListed 是无 path 限定时附在 diff 后面的未跟踪文件上限。
// 整仓未跟踪可能上万，不封顶会把真正的 diff 挤出上下文。
const maxUntrackedListed = 40

// maxLogLimit 是 log 返回条数的硬顶（0.0.06）。
const maxLogLimit = 50

// Tool 是只读 git 工具。
type Tool struct {
	root string
}

// New 构造工具，root 为仓库工作目录。
func New(root string) *Tool { return &Tool{root: root} }

// Name 实现工具端口。
func (t *Tool) Name() string { return "git" }

// Description 实现工具端口。
func (t *Tool) Description() string {
	return "只读查看 git 仓库：status（变更列表）/ diff（相对 HEAD 的差异：含已暂存，不含未跟踪；没有 path 时结果末尾另附未跟踪文件清单。仓库还没有提交时退回未暂存差异并写明）/ log（最近提交）"
}

// Schema 实现工具端口。
func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["status", "diff", "log"], "description": "diff 相对 HEAD（含已暂存，不含未跟踪；无 path 时结果末尾另附未跟踪清单）"},
    "path": {"type": "string", "description": "可选：限定单个路径（status/diff/log 都支持）"},
    "limit": {"type": "integer", "description": "log 时可选，返回条数上限（默认 10，最大 50）"}
  },
  "required": ["action"]
}`)
}

// Execute 实现工具端口（内部超时 + 输出有界 + 业务失败走 IsError）。
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (res tools.ToolResult, err error) {
	var a struct {
		Action string `json:"action"`
		Path   string `json:"path"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return businessErrf("invalid arguments: %v", err), nil
	}
	// 卡片语义标签："git status" 式子命令作主标签（defer 覆盖全部返回路径）
	defer func() {
		if res.Title == "" {
			res.Op = "git"
			res.Title = "git " + a.Action
		}
	}()

	gitArgs, err := buildArgs(a.Action, a.Path, a.Limit)
	if err != nil {
		return businessErrf("%v", err), nil
	}

	runCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	// 模型看的 diff 是相对 HEAD（含已暂存），并在无 path 时附上未跟踪清单。
	// Diff()（裸 git diff，只有未暂存）不走这条，提交说明仍用 DiffHEAD。
	if a.Action == "diff" {
		return t.modelDiff(runCtx, gitArgs, a.Path)
	}
	cmd := exec.CommandContext(runCtx, "git", gitArgs...)
	hideConsole(cmd)
	cmd.Dir = t.root
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf

	if err := cmd.Run(); err != nil {
		out := truncate(buf.String())
		if runCtx.Err() == context.DeadlineExceeded {
			return tools.ToolResult{Content: out + "\n[TIMEOUT: git killed]", IsError: true, TimedOut: true}, nil
		}
		return businessErrf("git %s failed: %s\n%v", a.Action, strings.TrimSpace(out), err), nil
	}
	out := strings.TrimSpace(buf.String())
	if out == "" {
		out = "(empty)"
	}
	return tools.ToolResult{Content: truncate(out)}, nil
}

// modelDiff 是给模型看的 diff：相对 HEAD（含已暂存），第一行写明口径。
// 仓库还没有提交时退回裸 git diff，避免 "bad revision HEAD" 把新仓库说成工具坏了。
// 无 path 时把未跟踪文件附在后面——它们不进 diff，但模型不看 status 就会当它们不存在。
func (t *Tool) modelDiff(ctx context.Context, gitArgs []string, path string) (tools.ToolResult, error) {
	usedHEAD := true
	if !t.headExists(ctx) {
		if ctx.Err() == context.DeadlineExceeded {
			return tools.ToolResult{Content: "[TIMEOUT: git killed]", IsError: true, TimedOut: true}, nil
		}
		gitArgs = dropRevisionHEAD(gitArgs)
		usedHEAD = false
	}
	out, err := t.runGit(ctx, gitArgs...)
	if err != nil {
		text := truncate(out)
		if ctx.Err() == context.DeadlineExceeded {
			return tools.ToolResult{Content: text + "\n[TIMEOUT: git killed]", IsError: true, TimedOut: true}, nil
		}
		return businessErrf("git diff failed: %s\n%v", strings.TrimSpace(text), err), nil
	}
	header := "（口径：相对 HEAD，含已暂存，不含未跟踪文件）"
	if !usedHEAD {
		header = "（口径：仓库还没有提交，无法相对 HEAD；以下只含未暂存差异，不含未跟踪文件）"
	}
	if path != "" {
		header += "；已按 path 限定"
	}
	body := strings.TrimSpace(out)
	if body == "" {
		body = "(empty)"
	}
	var b strings.Builder
	b.WriteString(header)
	b.WriteByte('\n')
	b.WriteString(body)
	if path == "" {
		t.appendUntracked(&b, ctx)
	}
	return tools.ToolResult{Content: truncate(b.String())}, nil
}

func (t *Tool) headExists(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", "HEAD")
	hideConsole(cmd)
	cmd.Dir = t.root
	return cmd.Run() == nil
}

func (t *Tool) runGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	hideConsole(cmd)
	cmd.Dir = t.root
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

// dropRevisionHEAD 只去掉 `diff HEAD` 里那个修订参数。路径恰好叫 HEAD 时在 "--" 后面，不动。
func dropRevisionHEAD(args []string) []string {
	if len(args) >= 2 && args[0] == "diff" && args[1] == "HEAD" {
		out := make([]string, 0, len(args)-1)
		out = append(out, args[0])
		out = append(out, args[2:]...)
		return out
	}
	return args
}

func (t *Tool) appendUntracked(b *strings.Builder, ctx context.Context) {
	out, err := t.runGit(ctx, "status", "--porcelain")
	if err != nil {
		fmt.Fprintf(b, "\n\n（未跟踪清单读取失败：%s）", strings.TrimSpace(out))
		return
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "?? ") {
			files = append(files, strings.TrimPrefix(line, "?? "))
		}
	}
	if len(files) == 0 {
		return
	}
	b.WriteString("\n\n未跟踪文件（不在上面的 diff 里）：\n")
	n := len(files)
	shown := files
	if n > maxUntrackedListed {
		shown = files[:maxUntrackedListed]
	}
	for _, f := range shown {
		b.WriteString(f)
		b.WriteByte('\n')
	}
	if n > maxUntrackedListed {
		fmt.Fprintf(b, "…还有 %d 个未跟踪文件\n", n-maxUntrackedListed)
	}
}

// ---- 0.3 最小能力：工作区提交路径（仅由壳层调用，不注册进模型工具） ----
//
// 边界（用户裁决，不可放宽）：本包对 git 的**改写能力只到 add -A 与 commit**。
// push / reset --hard / clean / rebase 等任何"丢弃改动或离开本机"的子命令
// 在这里没有实现路径——扩展时必须重新评审，而不是顺手加参数。

// runGitCmd 在 root 下执行一条 git 命令（超时 + 输出有界 + 业务失败转 error），
// 返回 trim 后的输出（大多数消费方只要正文）。
// 与测试文件里的 runGit 助手同名不同用，故这里带 Cmd 后缀。
func runGitCmd(root string, args ...string) (string, error) {
	out, err := runGitCmdRaw(root, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// runGitCmdRaw 是 runGitCmd 的不 trim 版本：porcelain 的状态列以空格占位
// （" M b.go" 的首列是暂存区），trim 会吃掉首列导致解析错位（测试抓到）。
func runGitCmdRaw(root string, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", args...)
	hideConsole(cmd)
	cmd.Dir = root
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		out := truncate(buf.String())
		if runCtx.Err() == context.DeadlineExceeded {
			return out, fmt.Errorf("git %s 超时（已终止）", args[0])
		}
		return out, fmt.Errorf("git %s 失败：%s", args[0], strings.TrimSpace(out))
	}
	return buf.String(), nil
}

// StatusShort 返回 porcelain 状态（含 "## 分支" 头行）：提交说明生成用它判断
// "有没有变更、在哪个分支"。
func StatusShort(root string) (string, error) {
	return runGitCmd(root, "status", "--porcelain", "-b")
}

// Diff 返回未暂存差异（有界，裸 git diff）。提交说明不要用它（看不见已暂存），用 DiffHEAD。
// 模型工具的 diff 动作也不走这里：那条路径是 diff HEAD + 未跟踪清单（见 modelDiff）。
func Diff(root string) (string, error) {
	out, err := runGitCmd(root, "diff")
	if err != nil {
		return "", err
	}
	return truncate(out), nil
}

// DiffHEAD 返回工作区相对 HEAD 的全部差异（**含已暂存块**，有界）。
// 提交说明的原料必须是它：add -A 会把"已暂存 + 未暂存 + 未跟踪"一起收进同一次
// 提交，说明若只看裸 diff，用户确认时看到的变更与实际提交的就不是同一份
// （0.0.30 用户审查 R1）。未跟踪文件（??）不在 diff 里，由调用方把 porcelain
// 的路径写进提示词补齐。
func DiffHEAD(root string) (string, error) {
	out, err := runGitCmd(root, "diff", "HEAD")
	if err != nil {
		return "", err
	}
	return truncate(out), nil
}

// StageAllAndCommit 执行 git add -A + git commit -m message，返回可展示的输出。
// message 经 argv 传入（不走 shell、无注入面）。
//
// 两步的失败语义不同，必须分开说（0.0.30 用户审查 R1）：
//   - add 失败：什么都没变，索引保持原样；
//   - commit 失败：**索引已被全部暂存**（add 已成功）——用户若不知道，下一次
//     commit 会把这一堆东西一起带走，或以为"没生效"而重复操作。
func StageAllAndCommit(root, message string) (string, error) {
	if _, err := runGitCmd(root, "add", "-A"); err != nil {
		return "", fmt.Errorf("git add 失败，索引未变动：%w", err)
	}
	out, err := runGitCmd(root, "commit", "-m", message)
	if err != nil {
		return "", fmt.Errorf("git commit 失败（注意：git add -A 已成功，本工作区的全部改动现已处于暂存区，未提交；修好原因后可直接重新提交）：%w", err)
	}
	return out, nil
}

// checkRelPath 校验模型/界面传入的路径限定在会话根内（0.0.30 用户审查 R3）。
//
// 为什么需要：`git … -- <path>` 的 pathspec 认 "../"（git 层面合法），而会话根
// 常常只是仓库的子目录——`../sibling` 会读到**同一仓库里、工作区之外**的兄弟
// 目录。fstool.resolve 已经拦了 ../ 与越界符号链接，git 读路径此前没接同一道闸。
// 只做前缀/形状校验（纯函数、可测），符号链接越界由 resolve 那侧兜。
func checkRelPath(path string) error {
	if path == "" {
		return nil
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, "/") {
		return fmt.Errorf("path 必须是工作区内的相对路径（收到 %q）", path)
	}
	// 归一化后若以 ".." 开头即越界；Windows 的反斜杠一并归一
	norm := strings.ReplaceAll(path, "\\", "/")
	clean := pathpkg.Clean(norm)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("path 越出工作区（收到 %q）", path)
	}
	return nil
}

// buildArgs 将动作映射为只读 git 参数（白名单式，绝不拼接任意用户输入到 shell）。
// path 统一先过 checkRelPath（0.0.30 用户审查 R3）：../ 与绝对路径不进 git。
func buildArgs(action, path string, limit int) ([]string, error) {
	if err := checkRelPath(path); err != nil {
		return nil, err
	}
	switch action {
	case "status":
		// -b（0.0.11）：带上 "## 分支" 头行——顶栏要显示当前分支，模型也该知道
		// 自己在哪条分支上干活；仍是纯只读查询（-b 只加一个头行，不改行为）。
		args := []string{"status", "--porcelain", "-b"}
		if path != "" {
			args = append(args, "--", path)
		}
		return args, nil
	case "diff":
		// 相对 HEAD：已暂存的改动也要让模型看见。仓库还没有提交时由 modelDiff 退回裸 diff。
		args := []string{"diff", "HEAD"}
		if path != "" {
			args = append(args, "--", path)
		}
		return args, nil
	case "log":
		if limit <= 0 {
			limit = 10
		}
		if limit > maxLogLimit {
			// 硬顶（0.0.06）：log --oneline 单行虽小，但上限封顶失控查询；
			// 需要更多历史的场景应引导模型缩小 path 范围
			limit = maxLogLimit
		}
		args := []string{"log", "--oneline", "-n", fmt.Sprint(limit)}
		// path 限定（0.0.06）：与 status/diff 同语义——pathspec 必须跟在 "--"
		// 之后，否则文件名恰与分支/选项同名时会被 git 误解
		if path != "" {
			args = append(args, "--", path)
		}
		return args, nil
	default:
		return nil, fmt.Errorf("unknown action %q (want status/diff/log)", action)
	}
}

// truncateToBytes 截断到 n 字节并回退到 UTF-8 边界（0.2.35 审计#8）：
// 按字节直接切会把中文切成非法 UTF-8，卡片与 diff 出现乱码。
func truncateToBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// truncate 截断超长输出（0.0.06 改头尾保留）：git diff/status/log 的关键信息
// 两头都有——diff 头部是文件清单、尾部是最新文件的变更块。只留头部会把
// 后面的文件变更整段截没，模型以为改动不存在。标注中间丢失字节数。
func truncate(s string) string {
	if len(s) <= outputLimit {
		return s
	}
	return tools.HeadTail(s, outputLimit)
}

func businessErrf(format string, a ...any) tools.ToolResult {
	return tools.ToolResult{Content: fmt.Sprintf(format, a...), IsError: true}
}

// StatusEntry 是结构化的 git status 条目（Git 面板用）：
// X/Y 是 porcelain 两列状态码（?? = 未跟踪；重命名记录已拆为新路径）。
type StatusEntry struct {
	Path      string `json:"path"`
	X         string `json:"x"`
	Y         string `json:"y"`
	Untracked bool   `json:"untracked"`
}

// StatusFiles 返回工作区变更文件的结构化清单（只读；未跟踪文件单列）。
// 无变更返回空切片（不是错误——面板的"干净"态靠它表达）。
// 解析 --porcelain -z 的 NUL 分隔输出；畸形行跳过（坏一行不牵连整个面板）。
func StatusFiles(root string) ([]StatusEntry, error) {
	out, err := runGitCmdRaw(root, "status", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	parts := strings.Split(out, "\x00")
	var entries []StatusEntry
	for i := 0; i < len(parts); i++ {
		rec := parts[i]
		if strings.TrimSpace(rec) == "" || len(rec) < 4 {
			continue
		}
		x, y := string(rec[0]), string(rec[1])
		path := rec[3:]
		// 重命名/拷贝：XY 行后跟 origPath 记录，下一个记录才是新路径
		if (x == "R" || x == "C" || y == "R" || y == "C") && i+1 < len(parts) {
			i++
			path = parts[i]
		}
		entries = append(entries, StatusEntry{Path: path, X: x, Y: y, Untracked: x == "?" && y == "?"})
	}
	return entries, nil
}

// DiffFile 返回单个文件相对 HEAD 的未暂存 diff（Git 面板点文件展示）。
// 与 Diff 同规：先过路径闸（0.0.30 R3），返回前 truncate（0.0.30 R3——
// 此前直接 return runGitCmd，注释写着"与 Diff 同规"但没接 truncate，
// 大文件 diff 会整份进界面）。
func DiffFile(root, path string) (string, error) {
	if err := checkRelPath(path); err != nil {
		return "", err
	}
	out, err := runGitCmd(root, "diff", "--", path)
	if err != nil {
		return "", err
	}
	return truncate(out), nil
}

// DiffFileHEAD 返回单个文件相对 HEAD 的**全部**改动（含已暂存，有界）。
//
// 为什么需要它（0.0.30 审查 R2）：DiffFile 跑 `git diff -- path`，**只有未暂存
// 部分**。一个"已暂存、工作区已干净"的文件在 Git 面板点开是空的——而提交说明走
// DiffHEAD（含已暂存），于是人在面板里看不到模型写进说明的那些行，两处口径不一致。
// 要看"这次提交会包含什么"，必须用 HEAD 口径。
//
// 围栏与有界同 DiffFile（checkRelPath + truncate），别因为多一个方法就少一道闸。
func DiffFileHEAD(root, path string) (string, error) {
	if err := checkRelPath(path); err != nil {
		return "", err
	}
	out, err := runGitCmd(root, "diff", "HEAD", "--", path)
	if err != nil {
		return "", err
	}
	return truncate(out), nil
}
