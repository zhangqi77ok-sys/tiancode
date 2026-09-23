// Package harness 是 DeepSeek Harness 哲学的运行时门面：把散装的 Agent 运行时能力
// （沙箱隔离 sandbox、SafetyRail 前置防护 host.Registry 中的 rail、loop 工具路由/ReAct、
// agent.TDD 自纠、memory 上下文装配）组合成单一可装配的运行体。
//
// 设计要点：执行内核 loop 保持无状态（不引入 session/memory/agent 依赖，见契约 A11），
// Harness 负责持有其依赖并在 BuildEngine 中装配 MCP 调用与 TDD 校验回调。
// TDD 校验未过时，Harness 自动将已写文件回退到最近影子快照（状态回溯安全网，见 A13）。
package harness

import (
	"context"

	"tiancode/internal/agent"
	"tiancode/internal/core/loop"
	"tiancode/internal/core/sandbox"
	"tiancode/internal/host"
	"tiancode/internal/mcp"
)

// Harness 运行时装配体
type Harness struct {
	registry    *host.Registry
	gateway     loop.InteractionGateway
	sandbox     *sandbox.Sandbox
	snapshotMgr *sandbox.SnapshotManager
	mcpManager  *mcp.Manager
}

// New 构造 Harness 运行时
func New(reg *host.Registry, gw loop.InteractionGateway, sb *sandbox.Sandbox, sm *sandbox.SnapshotManager, mgr *mcp.Manager) *Harness {
	return &Harness{
		registry:    reg,
		gateway:     gw,
		sandbox:     sb,
		snapshotMgr: sm,
		mcpManager:  mgr,
	}
}

// BuildEngine 装配执行引擎：注入 MCP 调用与 TDD 校验（含失败时自动回退）。
// 对应 A14：mcpManager 与引擎重建必须配对——切换工作区应经 App.rebindMCPManager，
// 不可单独重赋 a.mcpManager 而遗漏引擎重建。
func (h *Harness) BuildEngine(workspace string) *loop.ExecutionEngine {
	return loop.NewExecutionEngine(h.registry, h.gateway,
		func(ctx context.Context, name string, args map[string]any) (string, error) {
			return h.mcpManager.CallTool(ctx, name, args)
		},
		func(writtenFile string) (string, bool) {
			report, err := agent.RunTDDValidation(workspace)
			if err != nil {
				return err.Error(), false
			}
			out := report.Output
			if writtenFile != "" {
				out = writtenFile + "\n" + out
			}
			if report.Status != "PASS" && writtenFile != "" && h.snapshotMgr != nil {
				// 状态回溯安全网：TDD 未过时把已写文件回退到最近影子快照
				if rbErr := h.snapshotMgr.RollbackLatest(writtenFile); rbErr != nil {
					out += "\n[harness] 自动回退失败: " + rbErr.Error()
				} else {
					out += "\n[harness] TDD 未过，已自动回退 " + writtenFile
				}
			}
			return out, report.Status == "PASS"
		},
	)
}
