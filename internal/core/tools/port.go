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
	// Undo 保存"恢复这一次写入前"的数据（仅供 UI/后端恢复用，**绝不进模型上下文**，
	// 与 Diff 同纪律但更严格：不透传给模型消息，也不出现在 Content 里）。
	// 写入成功时由 write/replace 填充；旧内容超过上限时为 nil，改由 UndoNote 说明。
	Undo *UndoData
	// UndoNote 撤销不可用时的卡片说明（如"旧内容超过上限，这次无法恢复"）。
	// 仅 UI 可见；空串 = 无说明。
	UndoNote string
}

// UndoData 是一次文件写入的撤销快照。
// OldContent 是写入前的**全文**（新建文件为空且 OldExists=false，与"写入空内容"区分）；
// NewSHA256 是写入后内容的哈希——恢复前用它检测"文件被人改过"（不一致即拒绝，
// 宁可不恢复也不覆盖用户的改动）。Cap：旧内容超过写入上限时不生成（UndoNote 说明），
// 绝不为恢复把超大文件再读进内存。
type UndoData struct {
	Path       string `json:"path"`       // 工作区相对路径（恢复时在后端会话根下重新解析）
	OldExists  bool   `json:"old_exists"` // 写入前文件是否存在（false = 新建，恢复=删除）
	OldContent string `json:"old_content"`
	NewSHA256  string `json:"new_sha256"` // hex
}

// ProgressSink 是可选的执行进度端口：实现它的工具可在**执行中**把已捕获的
// 部分输出推给界面（0.0.07：长命令几分钟黑盒 → 卡片随过程增长）。
// 契约：cb 可能为 nil（清除）；实现方必须自行节流（如 500ms/次）且在执行结束
// 后停止调用；partial 只是过程快照，模型上下文仍只收终态的完整结果。
type ProgressSink interface {
	SetProgress(cb func(partial string))
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
