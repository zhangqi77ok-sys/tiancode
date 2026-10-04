// git 用例（0.3 最小能力）：根据本轮 diff 生成提交说明；确认后 add+commit。
//
// 边界：改写路径只有 add -A 与 commit 两条（见 gittool 包头注释——push/reset/clean
// 没有实现路径）；commit 必须由前端确认框放行后才到这里，本层不做任何"代确认"。
package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/gittool"
)

// commitMsgMaxBytes 是提交说明的长度上限（字节）：说明不是变更日志，超长只会让人跳过阅读。
// 注意单位是**字节**不是字数——中文一字三字节，500 字节约 166 字。
// 截断必须走 truncateUTF8（0.0.30 用户审查 R2）：按字节直切会切在半个 rune 上，
// 提交说明变成乱码，且 commit -m 接受它、提交历史里永久留下乱码。
const commitMsgMaxBytes = 500

// commitDiffMaxBytes 是喂给模型的 diff 上限（再大就该让人自己写说明了）。
const commitDiffMaxBytes = 32 * 1024

// truncateUTF8 截断到 n 字节并回退到 UTF-8 边界（0.0.30 用户审查 R2）。
// 与 gittool.truncateToBytes 同款纪律，但本包不能导入 gittool 的未导出版本——
// 这里的输入是模型产出的说明/喂给模型的 diff，两边都需要"绝不产生非法 UTF-8"。
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// suggestCommitPrompt 组装生成提交说明的请求文本：只输出说明本身，禁止多余客套。
// untracked 单列（0.0.30 R1）：未跟踪文件不在 diff 里（diff 只讲已跟踪文件的增删），
// 不点名的话模型看不见新文件——而 add -A 会把它们收进同一次提交。
func suggestCommitPrompt(status, diff string, untracked []string) string {
	var b strings.Builder
	b.WriteString("你是提交说明助手。根据下面的 git 变更生成一条简洁的中文提交说明：\n")
	b.WriteString("- 第一行为主题（不超过 50 字，祈使句，结尾不加句号）；\n")
	b.WriteString("- 如有必要，空一行后用少量要点补充；\n")
	b.WriteString("- 只输出说明本身，不要解释、不要代码块围栏。\n\n")
	b.WriteString("变更状态（porcelain）：\n" + status + "\n\n")
	if len(untracked) > 0 {
		b.WriteString("新增文件（未跟踪，会被一并提交，但上面的差异里没有它们的内容）：\n")
		for _, p := range untracked {
			b.WriteString("- " + p + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("差异：\n" + diff)
	return b.String()
}

// CommitSuggestion 是一次提交说明生成的完整结果（0.0.30 R1）。
//
// 为什么带 Files：说明是按"这份变更"生成的，而实际提交走 git add -A（范围更大）。
// 确认框必须列出**这次真正会纳入的每个路径**——用户点确认时看到的清单，
// 应当与提交进去的清单逐字一致，否则就是"说明与提交不是同一份变更"。
type CommitSuggestion struct {
	Message   string   `json:"message"`
	Files     []string `json:"files"`     // add -A 将纳入的全部路径（排序后）
	Model     string   `json:"model"`     // 生成说明用的模型（透明度：用户知道谁写的）
	Untracked []string `json:"untracked"` // 其中未跟踪的新文件（前端可标注"新增"）
}

// SuggestCommitMessage 读这场对话工作区的**全部未提交变更**（含已暂存），用当前
// 模型生成一条提交说明，并返回这次提交真正会纳入的路径清单。
// 不落账本、不写盘、不进入对话轮次——它是一个纯读的辅助动作。
// 没有变更 / 不是 git 仓库 / 没有模型渠道时显式报错（调用方展示原因）。
//
// 与 add -A 同范围（0.0.30 用户审查 R1）：说明的原料是 `git diff HEAD`（含已暂存）
// 加 porcelain 的未跟踪路径，Files 则取 porcelain 全量——三者是同一份变更的三种视图，
// 确认框列出 Files 后，用户看到的清单与提交进去的清单一致。
func (s *ChatService) SuggestCommitMessage(ctx context.Context, sessionID string) (CommitSuggestion, error) {
	var out CommitSuggestion
	if !s.sessionLedgerOnDisk(sessionID) {
		return out, fmt.Errorf("会话不存在或已删除：%s", sessionID)
	}
	root := s.sessionWorkspace(sessionID)
	if root == "" {
		return out, errors.New("这场对话没有工作区，无法读取 git 变更")
	}
	status, err := gittool.StatusShort(root)
	if err != nil {
		return out, fmt.Errorf("读取 git 状态失败：%w", err)
	}
	// 只有分支头行、没有变更行：没有可提交的东西，如实说明（不生成空话）
	lines := strings.Split(strings.TrimSpace(status), "\n")
	if len(lines) <= 1 {
		return out, errors.New("工作区没有未提交的变更")
	}
	// 变更清单 = porcelain 全量（add -A 的真实范围）；未跟踪单列（diff 里没有它们）
	entries, err := gittool.StatusFiles(root)
	if err != nil {
		return out, fmt.Errorf("读取 git 变更清单失败：%w", err)
	}
	files := make([]string, 0, len(entries))
	var untracked []string
	for _, e := range entries {
		files = append(files, e.Path)
		if e.Untracked {
			untracked = append(untracked, e.Path)
		}
	}
	sort.Strings(files)
	// 原料用 diff HEAD（含已暂存）——裸 diff 漏掉已暂存块，说明会与提交不一致
	diff, err := gittool.DiffHEAD(root)
	if err != nil {
		return out, fmt.Errorf("读取 git 差异失败：%w", err)
	}
	if len(diff) > commitDiffMaxBytes {
		diff = truncateUTF8(diff, commitDiffMaxBytes) + "\n…（差异过长已截断，说明按可见部分生成）"
	}
	s.mu.Lock()
	model := s.defaultModel
	s.mu.Unlock()
	if model == "" {
		return out, errors.New("尚未配置模型渠道，无法生成提交说明")
	}
	rt := llm.NewChatRuntime(s.gw, llm.TimeoutBudget{FirstByte: 3 * time.Minute, Total: 5 * time.Minute})
	ch, err := rt.Chat(ctx, llm.ChatRequest{
		Model:    model,
		Messages: []llm.Message{{Role: "user", Content: suggestCommitPrompt(status, diff, untracked)}},
	}, llm.DefaultRuntimePolicy())
	if err != nil {
		return out, fmt.Errorf("生成提交说明失败：%w", err)
	}
	var sb strings.Builder
	for c := range ch {
		if c.EndReason != llm.EndNone {
			if c.EndReason != llm.EndDone {
				if c.Err != nil {
					return out, fmt.Errorf("生成提交说明失败：%w", c.Err)
				}
				return out, errors.New("生成提交说明失败：上游流异常结束")
			}
			break
		}
		sb.WriteString(c.Delta)
	}
	msg := strings.TrimSpace(sb.String())
	if msg == "" {
		return out, errors.New("模型没有返回提交说明")
	}
	if len(msg) > commitMsgMaxBytes {
		// 按字节上限截，但必须退回 UTF-8 边界（中文绝不能被切在半个 rune 上）
		msg = truncateUTF8(msg, commitMsgMaxBytes)
	}
	return CommitSuggestion{Message: msg, Files: files, Model: model, Untracked: untracked}, nil
}

// GitStageAndCommit 在这场对话的工作区执行 git add -A + commit（前端确认框放行后调用）。
// 只此两条改写命令；message 为空/全空白显式拒绝——绝不编造默认说明。
func (s *ChatService) GitStageAndCommit(sessionID, message string) (string, error) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return "", errors.New("提交说明为空：请填写说明后再提交")
	}
	if !s.sessionLedgerOnDisk(sessionID) {
		return "", fmt.Errorf("会话不存在或已删除：%s", sessionID)
	}
	root := s.sessionWorkspace(sessionID)
	if root == "" {
		return "", errors.New("这场对话没有工作区，无法提交")
	}
	out, err := gittool.StageAllAndCommit(root, msg)
	if err != nil {
		return "", err
	}
	return out, nil
}

// ---------- Git 面板（0.0.24）：结构化变更清单 + 单文件 diff ----------

// GitStatusEntry 是 Git 面板的一行变更（透传 gittool 的结构化 porcelain）。
type GitStatusEntry = gittool.StatusEntry

// GitStatusFiles 返回这场对话工作区的变更文件清单（未跟踪单列；干净返回空切片）。
func (s *ChatService) GitStatusFiles(sessionID string) ([]GitStatusEntry, error) {
	root := s.sessionWorkspace(sessionID)
	if root == "" {
		return nil, fmt.Errorf("这场对话没有工作区，无法读取 Git 状态")
	}
	return gittool.StatusFiles(root)
}

// GitFileDiff 返回单个文件相对 HEAD 的未暂存 diff（有界截断与路径围栏同 Diff 规，
// 0.0.30 起真正接上——此前注释声称同规、实现直接返回未截断输出）。
func (s *ChatService) GitFileDiff(sessionID, path string) (string, error) {
	root := s.sessionWorkspace(sessionID)
	if root == "" {
		return "", fmt.Errorf("这场对话没有工作区，无法读取 Git diff")
	}
	return gittool.DiffFile(root, path)
}
