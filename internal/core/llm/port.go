// Package llm 定义大模型访问的端口与流式行为契约。
//
// 做什么：ProviderPort 抽象"对话补全的流式调用"，StreamChunk/EndReason 定义
// 流式传输的最小单元与互斥终态；本包不做任何 HTTP 实现。
// 被谁依赖：internal/core/agent（ReAct 循环）、internal/app（编排）。
// 依赖谁：仅 stdlib。
//
// 流式纪律参照 new-api relay/helper/stream_scanner.go（空闲看门狗 / 发送逃生 /
// EndReason 追踪 / 客户端断开反压）；具体实现位于 internal/platform/openaiprovider（M2）。
// 行为契约见 docs/CONTRACTS.md「流式三终态」。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// EndReason 标记一次流式调用的终态。终态互斥且整个流中恰好出现一个非 EndNone 块：
// 消费方必须依据 EndReason 区分"正常结束 / 错误 / 取消 / 超时"，
// 禁止以"流自然停止"来猜测终态（旧实现的断流卡死即源于此缺失）。
type EndReason int

const (
	// EndNone 零值：非终态块。为什么占用零值：使"EndReason != EndNone"天然表达"这是终态"，
	// 消费方一行判断即可统计终态且不与任何真实终态混淆。
	EndNone EndReason = iota
	// EndDone 上游正常完成（收到 finish_reason 或 [DONE]）。
	EndDone
	// EndError 上游或网络错误；随终态出现的 StreamChunk.Err 必填。
	EndError
	// EndCancelled 消费方取消（ctx.Done 触发）。
	EndCancelled
	// EndIdleTimeout 空闲超时：连续 idleTimeout 无任何新数据，判定上游挂起。
	// 为什么单独一态：挂起与显式错误的处置不同——前者应提示重试，后者应展示原因。
	EndIdleTimeout
)

// String 让终态在日志/错误里可读（0.0.09：诊断日志直接打 reason=done 而不是数字）。
func (r EndReason) String() string {
	switch r {
	case EndNone:
		return "none"
	case EndDone:
		return "done"
	case EndError:
		return "error"
	case EndCancelled:
		return "cancelled"
	case EndIdleTimeout:
		return "idle_timeout"
	}
	return fmt.Sprintf("reason(%d)", int(r))
}

// ToolEvent 是工具执行动态（UI 工具卡片的数据源；同时落账本供审计）。
type ToolEvent struct {
	Name    string // 工具名
	Status  string // "running" | "success" | "error"（running 仅实时流，不落账本）
	Summary string // 结果摘要（可截断）
	Content string // 全文；壳层 IPC 上限 64KiB，由 agent 截断
	// Diff 是编辑类工具的结构化 diff（无变更时为空）。
	// 为什么走结构化字段而非让 UI 解析 Content 文本：文本解析脆且一旦摘要被截断就丢信息；
	// 字段化后 UI 可按行着色，且契约由 tools.ToolResult.Diff 单向透传（ADR-0006）。
	Diff string
	// Title/Op 是工具卡片的语义标签（tools.ToolResult 同名透传）：主标签 + 动作类型。
	// 旧事件缺省为空，UI 回退工具名渲染。
	Title string
	Op    string
	// CallID 标识一次工具调用（0.0.06）：running 与终态事件同 ID——前端把
	// 终态更新到同一张卡上（卡随事件增长），而不是插一张新卡。
	CallID string
	// 撤销快照（0.0.07，仅供界面与后端恢复用——**绝不进模型上下文**）：
	// HasUndo 时前端可提供"恢复写入前"；UndoContent 故意不透传给前端——
	// 旧全文只留在账本里，恢复走后端（比对哈希防覆盖用户改动）。
	HasUndo    bool
	UndoPath   string
	UndoExists bool
	UndoNote   string
}

// TodoItem 是任务清单的单项。
type TodoItem struct {
	Text   string `json:"text"`
	Status string `json:"status"` // "pending" | "in_progress" | "done"
}

// TodoEvent 是任务清单动态（UI 任务卡数据源；agent 落账 EventTodo 供 Replay 恢复）。
// 每次携带全量清单：UI 原地更新一张卡，不逐次堆叠。
type TodoEvent struct {
	Items []TodoItem
}

// ContextEvent 是上下文治理读数（agent 每轮派生历史后上报一次，UI 油表用）：
// 预算与估算用量，以及本轮为压回预算而执行的折叠——界面必须能看见"上下文被裁过"，
// 折叠绝不静默（第 2 批）。
type ContextEvent struct {
	EstimatedTokens int `json:"estimatedTokens"` // 本轮送给模型的上下文估算（token，保守口径见 agent.estimateTextTokens）
	BudgetTokens    int `json:"budgetTokens"`    // 渠道声明的上限（token；0 = 未配置，界面不显示比例）
	// BudgetDefault 标记预算来自"渠道未声明时的保守默认值"（阶段 5-2）：界面要写明
	// 「未配置，按默认值」——不能让人以为渠道里填了这个数。
	BudgetDefault bool `json:"budgetDefault"`
	FoldedImages  int  `json:"foldedImages"` // 因超预算折叠的旧图片数（data URL → 路径说明）
	FoldedTools   int  `json:"foldedTools"`  // 因超预算折叠的旧 shell/写入回执数（→ 一行摘要）
	FoldedReads   int  `json:"foldedReads"`  // 因超预算额外折叠的只读结果数（收窄折叠窗口）
	Dropped       bool `json:"dropped"`      // 已无可再丢仍超预算（界面须标明"已折叠"）
}

// StreamChunk 是流式传输的最小单元。
// Delta 与 ToolCalls 可同时为空（例如仅携带 Usage 的收尾块）；
// ToolEvent/Todo/Context 非 nil 时为纯事件块（agent 产出，不经上游）；
// Err 非 nil 的块必为终态块；EndReason 仅在终态块上非零，其余块必须为零值。
type StreamChunk struct {
	Delta     string          // 文本增量
	Thinking  string          // 思考流增量（reasoning），可为空
	ToolCalls []ToolCallChunk // 工具调用增量分片（来自模型）
	ToolEvent *ToolEvent      // 工具执行动态（来自 agent）
	Todo      *TodoEvent      // 任务清单动态（来自 agent）
	Context   *ContextEvent   // 上下文治理读数（来自 agent，每轮一次）
	Usage     *Usage          // token 用量（上游返回时非 nil）
	Err       error           // 终态错误（仅 EndError 终态块非 nil）
	EndReason EndReason       // 仅终态块非零
}

// ToolCallChunk 是流式工具调用的增量分片；
// 同一 ID 的分片按到达顺序拼接（ArgumentsDelta 串接、Name 取首个非空）。
type ToolCallChunk struct {
	Index          int
	ID             string
	Name           string
	ArgumentsDelta string
}

// Usage 记录一次调用的 token 用量；上游未返回时为 nil。
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// ProviderPort 是大模型供应商端口：适配器实现它，内核只依赖它。
// 适配器清单见 docs/ARCHITECTURE.md；新增供应商 = 新增一个适配器文件，不得改动本端口。
type ProviderPort interface {
	// StreamChat 发起流式对话补全。
	// 契约：返回的 channel 在发出终态块后必须关闭；ctx 取消必须以 EndCancelled
	// 终态收束（不允许 goroutine 泄漏或连接悬挂）；实现内部必须有空闲看门狗，
	// 上游挂起以 EndIdleTimeout 收束而非永久阻塞。
	StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
}

// ToolDef 是提供给模型的工具定义（JSON Schema 形态，与具体供应商无关）。
// 供应商私有格式转换是适配器的职责（Convert 边界，参照 new-api Adaptor 接口）。
type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema
}

// ChatRequest 是一次对话补全请求的中性结构（与具体供应商无关）；
// 厂商私有格式转换是适配器的职责（Convert 边界，参照 new-api Adaptor 接口）。
type ChatRequest struct {
	Model    string
	Messages []Message
	Tools    []ToolDef // 模型可调用的工具定义；无工具时为空
}

// Message 是中性对话消息（OpenAI 兼容形态）。
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string // role=tool 时回填对应的调用 ID
	// Parts 非空时（仅 user 消息）content 用多模态数组形态（0.0.10）：
	// 文本 + 图片 data URL。空 Parts = 纯文本字符串 content（不改变既有请求形态）。
	Parts []ContentPart
}

// ContentPart 是多模态消息的一个片段。
type ContentPart struct {
	Type     string `json:"type"` // "text" | "image_url"
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"-"` // data URL；序列化时包进 image_url 对象
}

// ToolCall 是一条完整的工具调用请求（由增量分片拼接而成）。
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON 字符串
}
