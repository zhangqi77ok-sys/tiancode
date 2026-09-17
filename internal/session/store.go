package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tiancode/internal/config"
)

// SessionMessage 单条消息记录
type SessionMessage struct {
	ID       string         `json:"id"`
	Role     string         `json:"role"` // "user" | "assistant"
	Content  string         `json:"content"`
	Thinking string         `json:"thinking,omitempty"`
	Tool     *ToolExecution  `json:"tool,omitempty"`
	Tools    []ToolExecution `json:"tools,omitempty"`
	Time     string          `json:"time"`
}

// ToolExecution 算子执行历史
type ToolExecution struct {
	Name   string `json:"name"`
	Args   any    `json:"args"`
	Output string `json:"output"`
}

// TaskStatus 任务生命周期状态
type TaskStatus string

const (
	TaskStatusIdle        TaskStatus = "idle"
	TaskStatusRunning     TaskStatus = "running"
	TaskStatusCompleted   TaskStatus = "completed"
	TaskStatusCapped      TaskStatus = "capped"
	TaskStatusInterrupted TaskStatus = "interrupted"
	TaskStatusFailed      TaskStatus = "failed"
	TaskStatusPendingDiff TaskStatus = "pending_diff"
	TaskStatusTDDFailed   TaskStatus = "tdd_failed"
)

// TaskModel 会话挂载的最小任务模型
type TaskModel struct {
	Goal             string     `json:"goal"`
	Status           TaskStatus `json:"status"`
	ToolBudget       int        `json:"tool_budget"`
	ToolsUsed        int        `json:"tools_used"`
	Summary          string     `json:"summary"`
	TDDPassed        *bool      `json:"tdd_passed,omitempty"`
	PendingDiffFiles []string   `json:"pending_diff_files,omitempty"`
}

// ChatSession 会话完整历史实体
type ChatSession struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	Model     string           `json:"model"`
	Tag       string           `json:"tag,omitempty"`
	Workspace string           `json:"workspace,omitempty"`
	CreatedAt int64            `json:"created_at"`
	UpdatedAt int64            `json:"updated_at"`
	Messages  []SessionMessage `json:"messages"`
	Task      *TaskModel       `json:"task,omitempty"`
}

// SessionMeta 会话轻量摘要信息（供列表渲染）
type SessionMeta struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Model      string `json:"model"`
	Tag        string `json:"tag"`
	Time       string `json:"time"`
	Desc       string `json:"desc"`
	UpdatedAt  int64  `json:"updated_at"`
	Workspace  string `json:"workspace,omitempty"`
	TaskStatus string `json:"task_status,omitempty"`
}

// Store 会话本地磁盘管理器
type Store struct {
	mu      sync.RWMutex
	baseDir string
}

// NewStore 初始化会话存储，目录位于 ~/.tiancode/sessions/
func NewStore() (*Store, error) {
	dir := filepath.Join(config.UserDataDir(), "sessions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create sessions dir failed: %w", err)
	}

	s := &Store{baseDir: dir}
	return s, nil
}

func sameWorkspace(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func TitleFromFirstMessage(sess ChatSession) string {
	if t := strings.TrimSpace(sess.Title); t != "" && t != "新工程对话" && t != "新对话" {
		return t
	}
	for _, m := range sess.Messages {
		if m.Role != "user" {
			continue
		}
		line := strings.TrimSpace(m.Content)
		if line == "" {
			continue
		}
		r := []rune(line)
		if len(r) > 32 {
			return string(r[:32]) + "…"
		}
		return line
	}
	return "新对话"
}

// List 列出已保存会话摘要。workspace 非空时只返回该工作区（无 workspace 字段的旧文件视为未归属，不混入）。
func (s *Store) List(workspace string) []SessionMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil
	}

	metas := make([]SessionMeta, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || strings.Contains(name, ".tmp.") {
			continue
		}
		if filepath.Ext(name) != ".json" {
			continue
		}

		filePath := filepath.Join(s.baseDir, name)
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var sess ChatSession
		if err := json.Unmarshal(data, &sess); err == nil {
			if workspace != "" && sess.Workspace != "" && !sameWorkspace(sess.Workspace, workspace) {
				continue
			}
			if workspace != "" && sess.Workspace == "" {
				continue
			}
			if len(sess.Messages) == 0 {
				continue
			}
			desc := ""
			last := sess.Messages[len(sess.Messages)-1]
			r := []rune(strings.TrimSpace(last.Content))
			if len(r) > 36 {
				desc = string(r[:36]) + "…"
			} else {
				desc = string(r)
			}
			ts := sess.UpdatedAt
			if ts > 1e11 {
				ts = ts / 1000
			}
			taskStatus := ""
			if sess.Task != nil {
				taskStatus = string(sess.Task.Status)
			}
			metas = append(metas, SessionMeta{
				ID:         sess.ID,
				Title:      TitleFromFirstMessage(sess),
				Model:      sess.Model,
				Tag:        sess.Tag,
				Time:       formatSessionTime(ts),
				Desc:       desc,
				UpdatedAt:  sess.UpdatedAt,
				Workspace:  sess.Workspace,
				TaskStatus: taskStatus,
			})
		}
	}

	// 强制按更新时间降序排列，保证前端会话历史顺序严格一致
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedAt > metas[j].UpdatedAt
	})

	return metas
}

// CleanGoalPrompt 剥离前端策略前缀与附加约束标签，提取纯净的用户任务目标
func CleanGoalPrompt(raw string) string {
	s := strings.TrimSpace(raw)
	// 循环剥离开头的 [执行策略 ...] 或 [附加约束 ...] 等标签块
	for strings.HasPrefix(s, "[") {
		idx := strings.Index(s, "]")
		if idx == -1 {
			break
		}
		s = strings.TrimSpace(s[idx+1:])
	}
	if s == "" {
		return strings.TrimSpace(raw)
	}
	return s
}

// IsContinuationPrompt 检测用户输入是否为「接续上一次任务」的指令（支持长句与策略前缀剥离）
func IsContinuationPrompt(prompt string) bool {
	p := strings.ToLower(CleanGoalPrompt(prompt))
	p = strings.TrimRight(p, "!?.。！？~～ \r\n\t")
	if p == "" {
		return false
	}
	// 精确匹配常见接续短词
	switch p {
	case "继续", "继续执行", "继续做", "接着做", "接着来", "接着", "接着写", "继续写", "接着改", "继续改", "继续审查", "接着审查",
		"continue", "go on", "proceed", "next", "keep going", "ok继续", "好的继续", "继续吧", "接着推进", "继续推进":
		return true
	}
	// 前缀匹配（用户常用长句）：如「接着把审查写完」、「继续把刚才的代码改完」、「请继续完成...」、「继续优化 internal/core」
	prefixes := []string{
		"继续", "接着", "请继续", "请接着", "继续把", "接着把", "继续对", "接着对",
		"继续修改", "接着修改", "继续重构", "接着重构", "继续完善", "接着完善",
		"继续推进", "接着推进", "继续修复", "接着修复", "继续实现", "接着实现",
		"continue with", "continue to", "keep working on", "go on with",
	}
	for _, pre := range prefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// BuildContinuationContext 为接续任务构造提示词上下文，防止智能体推翻重来
func BuildContinuationContext(task *TaskModel, followUpInstruction ...string) string {
	if task == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n【接续上一次任务（铁律）】\n")
	sb.WriteString(fmt.Sprintf("- 既定总目标: %s\n", task.Goal))
	if len(followUpInstruction) > 0 && followUpInstruction[0] != "" {
		inst := strings.TrimSpace(followUpInstruction[0])
		if inst != "继续" && inst != "接着" && inst != "continue" {
			sb.WriteString(fmt.Sprintf("- 本轮用户追问/细化要求: %s\n", inst))
		}
	}
	if task.Summary != "" {
		sb.WriteString(fmt.Sprintf("- 上阶段已探明进展与未完成项: %s\n", task.Summary))
	}
	if len(task.PendingDiffFiles) > 0 {
		sb.WriteString(fmt.Sprintf("- 待确认变更文件: %s\n", strings.Join(task.PendingDiffFiles, ", ")))
	}
	sb.WriteString("- 执行要求: 请直接基于上一轮已探明的结果和已修改文件的基础上下一步执行，严禁推翻重来或重复进行顶层结构全盘勘探。请聚焦未完成部分直接推进！\n")
	return sb.String()
}

func formatSessionTime(unixSec int64) string {
	if unixSec <= 0 {
		return ""
	}
	t := time.Unix(unixSec, 0)
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	if t.Year() == now.Year() {
		return t.Format("01-02 15:04")
	}
	return t.Format("2006-01-02")
}

// sanitizeID 防御会话 ID 路径穿越 (Path Traversal)，只允许合法基名
func sanitizeID(id string) (string, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "", fmt.Errorf("session id cannot be empty")
	}
	clean := filepath.Base(filepath.Clean(trimmed))
	if clean == "." || clean == "/" || clean == "\\" || clean != trimmed {
		return "", fmt.Errorf("invalid session id format: %s", id)
	}

	// 拦截 Windows 设备保留字 (无论大小写与是否带后缀)
	upper := strings.ToUpper(clean)
	baseUpper := strings.TrimSuffix(upper, filepath.Ext(upper))
	switch baseUpper {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return "", fmt.Errorf("reserved device name cannot be used as session id: %s", id)
	}

	// 拦截非法文件名字符
	if strings.ContainsAny(clean, `<>:"/\|?*`+"\x00") {
		return "", fmt.Errorf("session id contains illegal characters: %s", id)
	}

	return clean, nil
}

// Get 获取单条会话完整历史
func (s *Store) Get(id string) (*ChatSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	safeID, err := sanitizeID(id)
	if err != nil {
		return nil, err
	}

	filePath := filepath.Join(s.baseDir, fmt.Sprintf("%s.json", safeID))
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("session [%s] not found: %w", safeID, err)
	}

	var sess ChatSession
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// Save 物理持久化单条会话至本地磁盘
func (s *Store) Save(sess ChatSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	safeID, err := sanitizeID(sess.ID)
	if err != nil {
		return err
	}
	sess.ID = safeID

	if sess.UpdatedAt == 0 {
		sess.UpdatedAt = time.Now().Unix()
	}
	if sess.CreatedAt == 0 {
		sess.CreatedAt = sess.UpdatedAt
	}
	sess.Title = TitleFromFirstMessage(sess)

	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}

	filePath := filepath.Join(s.baseDir, fmt.Sprintf("%s.json", safeID))
	return atomicWriteSession(filePath, data)
}

func atomicWriteSession(filePath string, data []byte) error {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dir, fmt.Sprintf(".%s.tmp_*", filepath.Base(filePath)))
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleaned := false
	defer func() {
		if !cleaned {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	// 原子替换
	if err := os.Rename(tmpPath, filePath); err != nil {
		// Windows: 若目标文件已存在可能报错 AccessDenied，尝试备份式替换或安全覆盖
		// 严禁直接无备份删除原文件
		return fmt.Errorf("session atomic rename failed: %w", err)
	}

	cleaned = true
	return nil
}

// Delete 删除指定会话
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	safeID, err := sanitizeID(id)
	if err != nil {
		return err
	}

	filePath := filepath.Join(s.baseDir, fmt.Sprintf("%s.json", safeID))
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// UpdateTag 增量更新会话标签
func (s *Store) UpdateTag(sessionID string, tag string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateSessionLocked(sessionID, func(sess *ChatSession) {
		sess.Tag = tag
		sess.UpdatedAt = time.Now().Unix()
	})
}

// AppendMessage 增量追加消息
func (s *Store) AppendMessage(sessionID string, msg SessionMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateSessionLocked(sessionID, func(sess *ChatSession) {
		sess.Messages = append(sess.Messages, msg)
		sess.UpdatedAt = time.Now().Unix()
	})
}

// UpdateTask 增量更新任务状态
func (s *Store) UpdateTask(sessionID string, updater func(task *TaskModel)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateSessionLocked(sessionID, func(sess *ChatSession) {
		if sess.Task == nil {
			sess.Task = &TaskModel{
				Goal:   "",
				Status: TaskStatusIdle,
			}
		}
		updater(sess.Task)
		sess.UpdatedAt = time.Now().Unix()
	})
}

func (s *Store) updateSessionLocked(sessionID string, mutator func(*ChatSession)) error {
	safeID, err := sanitizeID(sessionID)
	if err != nil {
		return err
	}
	path := filepath.Join(s.baseDir, safeID+".json")
	
	// 读取当前状态
	data, err := os.ReadFile(path)
	var sess ChatSession
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		// 新建
		sess = ChatSession{
			ID:        safeID,
			Messages:  make([]SessionMessage, 0),
			CreatedAt: time.Now().Unix(),
		}
	} else {
		if err := json.Unmarshal(data, &sess); err != nil {
			return err
		}
	}
	
	// 突变
	mutator(&sess)
	sess.Title = TitleFromFirstMessage(sess)
	
	// 写回
	out, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteSession(path, out)
}
