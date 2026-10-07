// 工作区检查命令（第 8 批）：回合终态 / 写入文件之后跑一次用户配的命令，
// 把输出里的 path:line(:col) 收成可点列表，并在下一轮给模型附一条
// **仅本次请求可见**的说明（不进账本、不进系统提示）。
//
// 为什么绝不猜命令：跑什么检查是每个仓库自己的事（go test / npm test / make check…），
// 猜错等于替用户执行了他没要求的动作。未配置 = 任何时候都不跑。
package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/tools"
	"tiancode/internal/platform/applog"
	"tiancode/internal/platform/oemtext"
)

// checkTimeout 是检查命令的固定超时（第 8 批：60 秒）。超时不编诊断，只把已捕获输出照实给出。
const checkTimeout = 60 * time.Second

// maxAutoFixTurns 是检查失败后自动定向修复的轮数预算（0.0.34 自愈循环）。
// 为什么 2：自愈是"给模型一次当场纠错的机会"，不是无限重跑——持续红的检查
// 大多是模型修不动的问题（或根本不是模型的锅），2 轮封顶把最坏代价框死；
// 预算在检查通过或真实用户消息发送时重置（新的一手重新计数）。
const maxAutoFixTurns = 2

// CheckRef 是输出里的一处位置引用（界面点它 = 第 6 项那个"打开"入口）。
type CheckRef struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Text string `json:"text"`
}

// CheckResult 是一次检查的结果（Skipped = 命令为空或已有一次在跑，什么都没发生）。
type CheckResult struct {
	SessionID string     `json:"sessionID"`
	Command   string     `json:"command"`
	Output    string     `json:"output"`
	Refs      []CheckRef `json:"refs"`
	Failed    bool       `json:"failed"`
	TimedOut  bool       `json:"timedOut"`
	Skipped   bool       `json:"skipped"`
	At        int64      `json:"at"` // Unix 毫秒
}

// checkNoteRawLimit 是"失败但解析不出位置"时附给模型的原文上限（头尾保留）。
const checkNoteRawLimit = 4 * 1024

// checkRefColonRe：`path:line` 或 `path:line:col`。盘符冒号单独放行（`D:\a\f.go:3:1`），
// 其余路径不许含空白与冒号。checkRefParenRe：vue-tsc / tsc 的 `file(line,col):`。
// 路径还要"像路径"（含 / . \）——避免把说明文字里的编号当引用。
var checkRefColonRe = regexp.MustCompile(`((?:[A-Za-z]:)?[^\s:]+?):(\d+)(?::(\d+))?:(.*)`)
var checkRefParenRe = regexp.MustCompile(`((?:[A-Za-z]:)?[^\s:()]+)\((\d+)(?:,(\d+))?\):\s*(.*)`)

var checkPathLike = regexp.MustCompile(`[./\\]`)

// ParseCheckRefs 从命令输出里收集位置引用（纯函数：可单测，也是"列表与附注同源"的保证）。
func ParseCheckRefs(output string) []CheckRef {
	var refs []CheckRef
	seen := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		m := matchCheckRef(strings.TrimSpace(line))
		if m == nil || !checkPathLike.MatchString(m[1]) {
			continue
		}
		ref := CheckRef{Path: m[1], Text: strings.TrimSpace(m[4])}
		fmt.Sscanf(m[2], "%d", &ref.Line)
		if m[3] != "" {
			fmt.Sscanf(m[3], "%d", &ref.Col)
		}
		key := fmt.Sprintf("%s:%d:%d", ref.Path, ref.Line, ref.Col)
		if seen[key] {
			continue
		}
		seen[key] = true
		refs = append(refs, ref)
	}
	return refs
}

// matchCheckRef 先认 `file(line,col):`，再认 `path:line:col:`。两组捕获下标一致：
// 1 路径、2 行、3 列（可空）、4 说明。
func matchCheckRef(line string) []string {
	if m := checkRefParenRe.FindStringSubmatch(line); m != nil {
		return m
	}
	return checkRefColonRe.FindStringSubmatch(line)
}

// checkRunner 是唯一启动进程的出口（测试注入，避免真跑用户的检查命令）。
// 隐藏控制台、按与 shell 相同的规则解码：中文 Windows 上裸 CombinedOutput 会乱码，
// 桌面进程还会闪一个黑窗。
var checkRunner = func(ctx context.Context, name string, args []string, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	hideCheckConsole(cmd)
	out, err := cmd.CombinedOutput()
	return oemtext.Decode(out), err
}

// RunWorkspaceCheck 跑一次这场对话工作区的检查命令（第 8 批）。
// 命令为空 / 已有一次在跑 → Skipped=true，且**不产生任何进程**。
// 超时与命令失败都把已捕获的 stdout/stderr 原样带回（不编诊断）。
func (s *ChatService) RunWorkspaceCheck(sessionID string) (CheckResult, error) {
	root := s.sessionWorkspace(sessionID)
	ws := loadWorkspaceSettings(root)
	tmpl := strings.TrimSpace(ws.CheckCommand)
	if tmpl == "" {
		// 工作区设置没配：回退项目级默认（AGENTS.md frontmatter 的 check，0.0.43）。
		// 设置显式配置永远优先——用户机器上的临时覆盖高于仓库声明。
		tmpl = agentsCheckCommand(root)
	}
	if tmpl == "" {
		return CheckResult{Skipped: true}, nil
	}
	tokens := SplitArgv(tmpl)
	if len(tokens) == 0 {
		return CheckResult{Skipped: true}, nil
	}
	if !s.beginCheck(sessionID) {
		return CheckResult{Skipped: true}, nil // 已有一次在跑：跳过，不并行两份
	}
	defer s.endCheck(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	out, err := checkRunner(ctx, tokens[0], tokens[1:], root)
	res := CheckResult{
		SessionID: sessionID,
		Command:   tmpl,
		Output:    out,
		Refs:      ParseCheckRefs(out),
		Failed:    err != nil,
		// 超时按成因标注（不伪装成"命令失败"）
		TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded),
		At:       time.Now().UnixMilli(),
	}
	if res.TimedOut {
		res.Output += fmt.Sprintf("\n(检查命令超过 %s 已终止)", checkTimeout)
	}
	s.setLastCheck(sessionID, res)
	if !res.Failed && !res.TimedOut {
		s.resetAutoFix(sessionID) // 检查变绿：自愈预算重置（新的一手重新计数）
	}
	return res, nil
}

// SetCheckHandler 注入检查结果的推送回调（壳层挂事件桥；nil = 只记内存不推送）。
func (s *ChatService) SetCheckHandler(fn func(CheckResult)) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	s.checkEmit = fn
}

// SetAutoFixHandler 注入"检查失败 → 自动定向修复回合"的启动回调（0.0.34 自愈循环）。
// 由壳层实现：调 SendFixTurn 拿流并用与 Send 同一条事件桥推给前端。
// nil = 功能关闭（测试/无壳层环境）。只放行不强制：回调内部认领失败（用户正在
// 发消息等）静默放弃——用户在驱动时绝不跟用户抢回合。
func (s *ChatService) SetAutoFixHandler(fn func(sessionID, reason string, attempt, max int)) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	s.autoFixEmit = fn
}

// afterTurnCheck 是回合收尾的检查入口：所有终态都跑检查并推送；只有正常收尾
// （EndDone）才允许触发自动修复——用户中断/看门狗/错误后再自动开新回合，
// 等于无视用户的"停"。
func (s *ChatService) afterTurnCheck(sessionID string, ended llm.EndReason) {
	res, err := s.RunWorkspaceCheck(sessionID)
	if err != nil {
		applog.Errorf("workspace check session=%s err=%v", sessionID, err)
		return
	}
	if !res.Skipped {
		s.checkMu.Lock()
		emit := s.checkEmit
		s.checkMu.Unlock()
		if emit != nil {
			emit(res)
		}
	}
	if ended != llm.EndDone || res.Skipped || !res.Failed || res.TimedOut || len(res.Refs) == 0 {
		return
	}
	s.maybeAutoFix(sessionID, res)
}

// maybeAutoFix 扣减自愈预算并启动修复回合。预算在检查通过（或跳过）与真实
// 用户消息发送时重置。
func (s *ChatService) maybeAutoFix(sessionID string, res CheckResult) {
	s.checkMu.Lock()
	if s.autoFixAttempts == nil {
		s.autoFixAttempts = map[string]int{}
	}
	s.autoFixAttempts[sessionID]++
	attempt := s.autoFixAttempts[sessionID]
	fn := s.autoFixEmit
	s.checkMu.Unlock()
	if attempt > maxAutoFixTurns {
		applog.Infof("auto-fix budget exhausted session=%s attempts=%d（不再自动修复；检查通过或发新消息后重置）", sessionID, attempt-1)
		return
	}
	if fn == nil {
		return
	}
	reason := fixTurnNote(res)
	applog.Infof("auto-fix start session=%s attempt=%d/%d", sessionID, attempt, maxAutoFixTurns)
	fn(sessionID, reason, attempt, maxAutoFixTurns)
}

// resetAutoFix 重置某会话的自愈预算（检查通过 / 真实用户消息发送时调用）。
func (s *ChatService) resetAutoFix(sessionID string) {
	s.checkMu.Lock()
	delete(s.autoFixAttempts, sessionID)
	s.checkMu.Unlock()
}

// fixTurnNote 是修复回合的系统留痕文本：写明为什么有这一轮、前几处位置。
// （完整列表与界面上的检查结果卡片同源；预算次数由事件载荷带，不进留痕。）
func fixTurnNote(res CheckResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "（系统）工作区检查未通过（%s 报告 %d 处问题），自动定向修复中。请优先修复下述位置，修完再让检查变绿：", res.Command, len(res.Refs))
	for i, r := range res.Refs {
		if i == 3 {
			b.WriteString("\n…其余位置见界面检查列表")
			break
		}
		pos := fmt.Sprintf("%s:%d", r.Path, r.Line)
		if r.Col > 0 {
			pos += fmt.Sprintf(":%d", r.Col)
		}
		fmt.Fprintf(&b, "\n- %s", pos)
		if r.Text != "" {
			b.WriteString("  " + r.Text)
		}
	}
	return b.String()
}

// SendFixTurn 启动一次系统定向修复回合（壳层在 SetAutoFixHandler 回调里调用）。
// 会话忙（用户正在发消息）→ 明确报错，自动修复绝不与用户抢回合。
func (s *ChatService) SendFixTurn(ctx context.Context, sessionID, reason string) (<-chan llm.StreamChunk, error) {
	return s.sendCoreMode(ctx, sessionID, "", nil, nil, reason, false)
}

// RunCheckAndEmit 跑一次检查并把结果推给壳层（第 8 批）。跳过与失败都不打扰对话本身：
// 跳过（未配置 / 已在跑）什么都不推，失败也只记日志（命令自身的输出照常带回）。
func (s *ChatService) RunCheckAndEmit(sessionID string) {
	res, err := s.RunWorkspaceCheck(sessionID)
	if err != nil {
		applog.Errorf("workspace check session=%s err=%v", sessionID, err)
		return
	}
	if res.Skipped {
		return
	}
	s.checkMu.Lock()
	emit := s.checkEmit
	s.checkMu.Unlock()
	if emit != nil {
		emit(res)
	}
}

// LastCheck 返回这场对话最近一次检查结果（无 = 零值）。
func (s *ChatService) LastCheck(sessionID string) CheckResult {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	return s.lastChecks[sessionID]
}

// checkNote 是"下一轮附给模型的说明"：标题写明不是用户原话，正文与界面列表一致。
// 为空表示什么都不附。
func (s *ChatService) checkNote(sessionID string) string {
	res := s.LastCheck(sessionID)
	if res.Skipped || res.Command == "" {
		return ""
	}
	if len(res.Refs) == 0 {
		// 命令红了但一个 path:line 都没有：自愈仍不启动，但模型必须看见原文，
		// 否则下一轮会当成检查没跑过。
		if !res.Failed && !res.TimedOut {
			return ""
		}
		body := strings.TrimSpace(res.Output)
		if body == "" {
			body = "(无输出)"
		} else if len(body) > checkNoteRawLimit {
			body = tools.HeadTail(body, checkNoteRawLimit)
		}
		return fmt.Sprintf("【工作区检查，不是用户原话】上一步结束后自动跑了 %s，命令未通过，但未能解析出位置。以下是命令输出（头尾保留）：\n%s", res.Command, body)
	}
	var b strings.Builder
	b.WriteString("【工作区检查，不是用户原话】上一步结束后自动跑了 ")
	b.WriteString(res.Command)
	b.WriteString("，以下是它报告的位置（与界面上的列表一致）：\n")
	for _, r := range res.Refs {
		pos := fmt.Sprintf("%s:%d", r.Path, r.Line)
		if r.Col > 0 {
			pos = fmt.Sprintf("%s:%d:%d", r.Path, r.Line, r.Col)
		}
		b.WriteString("- ")
		b.WriteString(pos)
		if r.Text != "" {
			b.WriteString("  ")
			b.WriteString(r.Text)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (s *ChatService) beginCheck(sessionID string) bool {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	if s.checkRunning == nil {
		s.checkRunning = map[string]bool{}
	}
	if s.checkRunning[sessionID] {
		return false
	}
	s.checkRunning[sessionID] = true
	return true
}

func (s *ChatService) endCheck(sessionID string) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	delete(s.checkRunning, sessionID)
}

func (s *ChatService) setLastCheck(sessionID string, res CheckResult) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	if s.lastChecks == nil {
		s.lastChecks = map[string]CheckResult{}
	}
	s.lastChecks[sessionID] = res
}
