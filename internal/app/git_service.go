// git 用例（0.3 最小能力）：根据本轮 diff 生成提交说明；确认后 add+commit。
//
// 边界：改写路径只有 add -A 与 commit 两条（见 gittool 包头注释——push/reset/clean
// 没有实现路径）；commit 必须由前端确认框放行后才到这里，本层不做任何"代确认"。
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/gittool"
)

// commitMsgMaxChars 是提交说明的长度上限：说明不是变更日志，超长说明只会让人跳过阅读。
const commitMsgMaxChars = 500

// commitDiffMaxBytes 是喂给模型的 diff 上限（再大就该让人自己写说明了）。
const commitDiffMaxBytes = 32 * 1024

// suggestCommitPrompt 组装生成提交说明的请求文本：只输出说明本身，禁止多余客套。
func suggestCommitPrompt(status, diff string) string {
	return "你是提交说明助手。根据下面的 git 变更生成一条简洁的中文提交说明：\n" +
		"- 第一行为主题（不超过 50 字，祈使句，结尾不加句号）；\n" +
		"- 如有必要，空一行后用少量要点补充；\n" +
		"- 只输出说明本身，不要解释、不要代码块围栏。\n\n" +
		"变更状态（porcelain）：\n" + status + "\n\n差异：\n" + diff
}

// SuggestCommitMessage 读这场对话工作区的未提交变更，用当前模型生成一条提交说明。
// 不落账本、不写盘、不进入对话轮次——它是一个纯读的辅助动作。
// 没有变更 / 不是 git 仓库 / 没有模型渠道时显式报错（调用方展示原因）。
func (s *ChatService) SuggestCommitMessage(ctx context.Context, sessionID string) (string, error) {
	if !s.sessionLedgerOnDisk(sessionID) {
		return "", fmt.Errorf("会话不存在或已删除：%s", sessionID)
	}
	root := s.sessionWorkspace(sessionID)
	if root == "" {
		return "", errors.New("这场对话没有工作区，无法读取 git 变更")
	}
	status, err := gittool.StatusShort(root)
	if err != nil {
		return "", fmt.Errorf("读取 git 状态失败：%w", err)
	}
	// 只有分支头行、没有变更行：没有可提交的东西，如实说明（不生成空话）
	lines := strings.Split(strings.TrimSpace(status), "\n")
	if len(lines) <= 1 {
		return "", errors.New("工作区没有未提交的变更")
	}
	diff, err := gittool.Diff(root)
	if err != nil {
		return "", fmt.Errorf("读取 git 差异失败：%w", err)
	}
	if len(diff) > commitDiffMaxBytes {
		diff = diff[:commitDiffMaxBytes] + "\n…（差异过长已截断，说明按可见部分生成）"
	}
	s.mu.Lock()
	model := s.defaultModel
	s.mu.Unlock()
	if model == "" {
		return "", errors.New("尚未配置模型渠道，无法生成提交说明")
	}
	rt := llm.NewChatRuntime(s.gw, llm.TimeoutBudget{FirstByte: 3 * time.Minute, Total: 5 * time.Minute})
	ch, err := rt.Chat(ctx, llm.ChatRequest{
		Model:    model,
		Messages: []llm.Message{{Role: "user", Content: suggestCommitPrompt(status, diff)}},
	}, llm.DefaultRuntimePolicy())
	if err != nil {
		return "", fmt.Errorf("生成提交说明失败：%w", err)
	}
	var sb strings.Builder
	for c := range ch {
		if c.EndReason != llm.EndNone {
			if c.EndReason != llm.EndDone {
				if c.Err != nil {
					return "", fmt.Errorf("生成提交说明失败：%w", c.Err)
				}
				return "", errors.New("生成提交说明失败：上游流异常结束")
			}
			break
		}
		sb.WriteString(c.Delta)
	}
	msg := strings.TrimSpace(sb.String())
	if msg == "" {
		return "", errors.New("模型没有返回提交说明")
	}
	if len(msg) > commitMsgMaxChars {
		msg = msg[:commitMsgMaxChars]
	}
	return msg, nil
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

// GitFileDiff 返回单个文件相对 HEAD 的未暂存 diff（有界截断与 Diff 同规）。
func (s *ChatService) GitFileDiff(sessionID, path string) (string, error) {
	root := s.sessionWorkspace(sessionID)
	if root == "" {
		return "", fmt.Errorf("这场对话没有工作区，无法读取 Git diff")
	}
	return gittool.DiffFile(root, path)
}
