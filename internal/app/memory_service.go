// 记忆用例（0.0.19）：每轮系统提示注入 + memory 工具装配。
package app

import (
	"errors"
	"fmt"
	"strings"

	"tiancode/internal/platform/memory"
)

// memorySection 组装每轮系统提示的记忆段（两级：全局 + 本项目）。
// 两级都空 = 空串（整段不出现——记忆是增量能力，没有记忆不能占上下文预算）。
// 纯对话会话（无工作区）是合法常态：没有项目记忆不是错误（ErrNoWorkspace 照常
// 注入全局侧）。其余读取失败上抛：与语气同纪律，静默会让用户以为模型记得
// 它其实忘掉的事。
func (s *ChatService) memorySection(root string) (string, error) {
	if s.memory == nil {
		return "", nil
	}
	global, err := s.memory.Lines(memory.ScopeGlobal, root)
	if err != nil {
		return "", fmt.Errorf("读取全局记忆失败：%w", err)
	}
	project, err := s.memory.Lines(memory.ScopeWorkspace, root)
	if err != nil && !errors.Is(err, memory.ErrNoWorkspace) {
		return "", fmt.Errorf("读取项目记忆失败：%w", err)
	}
	if len(global) == 0 && len(project) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("## 长期记忆（跨对话留存，按此作答；与用户本次的说法冲突时以本次为准）\n")
	if len(global) > 0 {
		b.WriteString("### 全局（用户偏好）\n")
		for _, l := range global {
			b.WriteString("- " + l + "\n")
		}
	}
	if len(project) > 0 {
		b.WriteString("### 本项目\n")
		for _, l := range project {
			b.WriteString("- " + l + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// memoryRoot 解析"记忆属于哪个工作区"：已落账会话用归属根，草稿回退顶栏根
// （与 ListWorkspaceDir 同语义——记忆面板里看到的项目侧必须和这场对话真实
// 会注入的项目侧是同一份）；都没有 = 纯对话，workspace 侧按 ErrNoWorkspace 呈空。
func (s *ChatService) memoryRoot(sessionID string) string {
	if root := s.sessionWorkspace(sessionID); root != "" {
		return root
	}
	return s.Workspace()
}

// memoryView 是记忆管理界面的两级视图（0.0.21）：全局 + 本项目各一组原文行。
type MemoryView struct {
	Global  []string `json:"global"`
	Project []string `json:"project"`
}

// MemoryLines 读两级记忆（壳层展示用）。纯对话 workspace 侧为空（不是错误）；
// 读取失败上抛，静默会让用户以为模型记得它其实忘掉的事。
func (s *ChatService) MemoryLines(sessionID string) (MemoryView, error) {
	var view MemoryView
	if s.memory == nil {
		return view, nil
	}
	root := s.memoryRoot(sessionID)
	global, err := s.memory.Lines(memory.ScopeGlobal, root)
	if err != nil {
		return view, fmt.Errorf("读取全局记忆失败：%w", err)
	}
	project, err := s.memory.Lines(memory.ScopeWorkspace, root)
	if err != nil && !errors.Is(err, memory.ErrNoWorkspace) {
		return view, fmt.Errorf("读取项目记忆失败：%w", err)
	}
	view.Global, view.Project = global, project
	return view, nil
}

// MemoryDelete 删除某 scope 的第 line 条（1 基，与模型侧 memory 工具同一锚点）。
// 越界显式报错；非法 scope 显式报错（壳层不翻译业务枚举）。
func (s *ChatService) MemoryDelete(sessionID, scope string, line int) error {
	if s.memory == nil {
		return errors.New("记忆功能未启用")
	}
	sc, err := parseMemoryScope(scope)
	if err != nil {
		return err
	}
	return s.memory.Delete(sc, s.memoryRoot(sessionID), line)
}

// MemoryClear 清空某 scope 的全部记忆（危险动作，前端确认后调用）。
func (s *ChatService) MemoryClear(sessionID, scope string) error {
	if s.memory == nil {
		return errors.New("记忆功能未启用")
	}
	sc, err := parseMemoryScope(scope)
	if err != nil {
		return err
	}
	return s.memory.Clear(sc, s.memoryRoot(sessionID))
}

// parseMemoryScope 把界面传来的 scope 字符串收敛为包内枚举。
func parseMemoryScope(scope string) (memory.Scope, error) {
	switch scope {
	case "global":
		return memory.ScopeGlobal, nil
	case "workspace":
		return memory.ScopeWorkspace, nil
	default:
		return "", fmt.Errorf("未知的记忆作用域：%q", scope)
	}
}
