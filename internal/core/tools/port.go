// Package tools 定义工具端口与执行契约。
//
// 做什么：ToolPort 抽象内核可调用的工具（fs/shell/git…），ToolResult 是统一返回。
// 被谁依赖：internal/core/agent（ReAct 循环）、internal/app。
// 依赖谁：仅 stdlib；具体工具实现在 internal/platform（M3/M4 落地）。
//
// 执行契约（"失败也一致"，细则见 docs/CONTRACTS.md）：
//   - 所有可变工具必须在实现内部对 ctx 施加超时（默认 120s，可配）；
//     旧实现 60s 硬超时强杀导致 npm install/go build 必死，此为根治点；
//   - 超时或中断必须返回已捕获的部分输出并置 TimedOut=true，不允许静默；
//   - 业务失败用 ToolResult.IsError 表达（模型可见、可继续推理）；
//     error 返回值仅用于工具机制本身故障（如注册表缺失）。
package tools

import (
	"context"
	"encoding/json"
)

// ToolResult 是工具执行的统一结果。
// Content 永远非空：成功时为输出，失败时为可读错误描述——
// 保证模型在任何失败场景下都能获得可继续推理的信息。
type ToolResult struct {
	Content  string
	IsError  bool
	TimedOut bool // 超时/被中断时为 true，Content 携带已捕获的部分输出
	// Diff 是编辑类工具产生的结构化 diff（无变更时为空串）。
	// 仅供 UI 展示（agent 透传到 llm.ToolEvent.Diff），模型上下文仍只用 Content——
	// 这样 diff 不额外消耗 token，也不改变模型可见的工具语义（ADR-0006）。
	Diff string
	// Title/Op 是工具卡片的语义标签（仅供 UI，不进模型上下文，与 Diff 同纪律）：
	// Title 主标签（文件名/命令首段/搜索词），Op 动作类型（read/write/edit/list/exec/search/git）。
	// 设计动因：Summary 只是 Content 的字节截断，UI 拿它渲染不出"install.go（修改）"；
	// 语义必须由最清楚自己干了什么的工具在生产侧结构化给出。旧账本事件缺此二字段，
	// UI 按空值回退工具名（可选字段，向后兼容）。
	Title string
	Op    string
}

// ToolPort 是工具端口：适配器实现它，内核只依赖它。
type ToolPort interface {
	// Name 返回工具唯一名（模型可见），如 fs / shell。
	Name() string
	// Description 返回给模型看的用途描述（供工具选择）。
	Description() string
	// Schema 返回参数的 JSON Schema（发给模型的工具定义）。
	Schema() json.RawMessage
	// Execute 执行工具。实现必须遵守包注释中的执行契约：
	// 内部超时可配、超时返回部分输出、业务失败走 ToolResult.IsError。
	Execute(ctx context.Context, args json.RawMessage) (ToolResult, error)
}
