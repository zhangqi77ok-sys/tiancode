// 方案模式（0.0.35）：用户点名"大任务先出方案再动手"。
// 开着发送的一轮是**只读调研**——写文件、跑命令、动浏览器、改扩展全部不在场，
// 模型调研完输出实施方案，用户确认后以普通回合执行（方案文本随用户消息带走）。
// 为什么做在注册表层而不是提示里：靠提示约束"不要写"是君子协定，工具不在场
// 才是结构保证（与"写入回执不进账本不可撤"完全不同的风险等级）。
package app

import (
	"context"
	"encoding/json"
	"fmt"

	"tiancode/internal/core/agent"
	"tiancode/internal/core/tools"
)

// planModePreface 是方案模式的系统说明段：拼在环境事实之后（applyExtensionPreface）。
// 逐字常量——同回合内不变是 prompt cache 的前提，与其他段同一纪律。
const planModePreface = `【方案模式】这一轮是**只读调研**阶段：写文件（fs write/replace）、执行命令（shell）、浏览器操作、扩展管理都不在场，想调也会被拒。
请先充分调研与任务相关的代码现状（read / symbols / diagnose / search / git 查看 / 读网页都可用），然后输出一份**可执行的实施方案**：
1. 要改哪些文件、各自改什么（关键处给出目标代码形态）；
2. 实施步骤（建议顺序与依赖）；
3. 怎么验证（跑什么测试/检查命令、预期看到什么）；
4. 风险与回退方式。
用户确认方案后会开新回合执行——方案阶段绝不假装已经动手，也不输出大段改后的完整文件。`

// planRegistry 构造方案模式的只读工具集：todo/ask_user（交互）+ webfetch/git/search
// （只读）+ fs 只读包装。shell/browser/memory/ext 全部不在场——MCP 工具语义未知，
// memory 的 append/delete 是写操作，宁可少给。
func (s *ChatService) planRegistry(st *sessionTools) (*tools.Registry, error) {
	registry := tools.NewRegistry()
	if err := registry.Register(agent.NewTodoTool()); err != nil {
		return nil, fmt.Errorf("register tool: %w", err)
	}
	if err := registry.Register(agent.NewAskUserTool()); err != nil {
		return nil, fmt.Errorf("register tool: %w", err)
	}
	if err := registry.Register(s.webFetch); err != nil {
		return nil, fmt.Errorf("register tool: %w", err)
	}
	if st != nil {
		for _, t := range []tools.ToolPort{st.git, st.search} {
			if t == nil {
				continue
			}
			if err := registry.Register(t); err != nil {
				return nil, fmt.Errorf("register tool: %w", err)
			}
		}
		if st.fs != nil {
			if err := registry.Register(planFS{inner: st.fs}); err != nil {
				return nil, fmt.Errorf("register tool: %w", err)
			}
		}
	}
	return registry, nil
}

// planFSActions 是方案模式下放行的 fs 动作（**白名单**：新增动作默认不出现，
// 与 isReadOnlyCall 的"白名单宁可窄"同一纪律）。
var planFSActions = map[string]bool{"read": true, "list": true, "tree": true, "diagnose": true, "symbols": true}

// planFS 是 fs 工具的只读视图：write/replace 及未来任何新动作一律业务拒绝。
type planFS struct{ inner tools.ToolPort }

func (p planFS) Name() string { return p.inner.Name() }

func (p planFS) Description() string {
	return "【方案模式 · 只读】" + p.inner.Description() + "（当前为方案模式：write/replace 不可用）"
}

func (p planFS) Schema() json.RawMessage { return p.inner.Schema() }

func (p planFS) Execute(ctx context.Context, raw json.RawMessage) (tools.ToolResult, error) {
	var a struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(raw, &a) != nil || !planFSActions[a.Action] {
		return tools.ToolResult{
			Content: fmt.Sprintf("方案模式下 fs 仅允许只读动作（%v）；写文件请先输出方案，用户确认后执行。", keysOf(planFSActions)),
			IsError: true,
		}, nil
	}
	return p.inner.Execute(ctx, raw)
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// 排序保证消息稳定（map 遍历无序）
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
