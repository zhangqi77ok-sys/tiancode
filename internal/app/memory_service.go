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

// MemoryLines 读两级记忆原文（壳层展示用；纯对话 workspace 侧为空）。
func (s *ChatService) MemoryLines(root string) (global, project []string, err error) {
	if s.memory == nil {
		return nil, nil, nil
	}
	if global, err = s.memory.Lines(memory.ScopeGlobal, root); err != nil {
		return nil, nil, err
	}
	if project, err = s.memory.Lines(memory.ScopeWorkspace, root); err != nil && !errors.Is(err, memory.ErrNoWorkspace) {
		return nil, nil, err
	}
	return global, project, nil
}
