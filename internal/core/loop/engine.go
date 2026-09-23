// Package loop 是 ReAct 双环自主执行内核，设计为无状态：
// 它接收已装配好的 EngineRequest（含 Messages / LLMTools / Strategy），
// 自身不持有会话或记忆。多轮会话历史的压缩与上下文窗口装配由领域服务
// internal/core/memory 负责，由应用宿主层（app_chat.go）编排后注入
// EngineRequest.Messages。这样 loop 不反向依赖 session/memory，保持可独立测试。
package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiancode/internal/host"
	"tiancode/internal/llm"
	v1 "tiancode/pkg/plugin/v1"
)

// EventType 引擎向前端派发的事件类型
type EventType string

const (
	EventChunk        EventType = "chunk"
	EventToolStart    EventType = "tool_start"
	EventToolEnd      EventType = "tool_end"
	EventDone         EventType = "done"
	EventError        EventType = "error"
	EventFilesChanged EventType = "files_changed"
	EventHitCap       EventType = "hit_cap"
	EventTDDResult    EventType = "tdd_result"
	EventChoice       EventType = "choice"
	EventConfirm      EventType = "confirm"
	EventUsage        EventType = "usage"
)

// ChoiceOption 用户选择题选项
type ChoiceOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// ChoicePayload 选项选择载荷
type ChoicePayload struct {
	SessionID   string         `json:"session_id"`
	RequestID   string         `json:"request_id"`
	Question    string         `json:"question"`
	Options     []ChoiceOption `json:"options"`
	AllowCustom bool           `json:"allow_custom"`
}

// ConfirmPayload 危险命令确认载荷
type ConfirmPayload struct {
	SessionID   string `json:"session_id"`
	RequestID   string `json:"request_id"`
	Tool        string `json:"tool"`
	ArgsPreview string `json:"args_preview"`
	Reason      string `json:"reason"`
}

// EngineEvent 引擎事件
type EngineEvent struct {
	Type         EventType       `json:"type"`
	DeltaContent string          `json:"delta_content,omitempty"`
	Thinking     string          `json:"thinking,omitempty"`
	ToolCallID   string          `json:"tool_call_id,omitempty"`
	ToolName     string          `json:"tool_name,omitempty"`
	ToolArgs     json.RawMessage `json:"tool_args,omitempty"`
	ToolOutput   string          `json:"tool_output,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	TDDPassed    *bool           `json:"tdd_passed,omitempty"`
	Choice       *ChoicePayload  `json:"choice,omitempty"`
	Confirm      *ConfirmPayload `json:"confirm,omitempty"`
	Usage        *v1.TokenUsage  `json:"usage,omitempty"`
}

// EngineRequest 用户推理请求
type EngineRequest struct {
	Model        string `json:"model"`
	Prompt       string `json:"prompt"`
	Provider     string `json:"provider,omitempty"`
	SessionID    string
	Endpoint     string
	APIKey       string
	SystemPrompt string
	Messages     []llm.Message
	LLMTools     []llm.ToolDef
	Strategy     string
	StrategyNote string
}

// AssembledToolCall 组装后的工具调用
type AssembledToolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

// HumanReply 人类干预的反馈数据
type HumanReply struct {
	OptionID   string
	CustomNote string
	Allow      bool
	Timeout    bool
}

// InteractionGateway 处理运行时的需要人类介入的操作（如危险命令确认、多项选择题等）
type InteractionGateway interface {
	RequestConfirm(ctx context.Context, sessionID, requestID, toolName, argsPreview, reason string) (bool, error)
	RequestChoice(ctx context.Context, sessionID, requestID, question string, options []ChoiceOption) (HumanReply, error)
}

// ExecutionEngine ReAct 双环自主执行引擎
type ExecutionEngine struct {
	registry *host.Registry
	mcpCall  func(ctx context.Context, name string, args map[string]any) (string, error)
	verify   func(writtenFile string) (output string, pass bool)

	gateway InteractionGateway
}

// NewExecutionEngine 构造执行引擎。
// MCPCall/Verify 在构造期即确定，禁止构造后从外部改写（消除导出可变字段，根治双构建隐患）。
func NewExecutionEngine(reg *host.Registry, gw InteractionGateway, mcpCall func(ctx context.Context, name string, args map[string]any) (string, error), verify func(writtenFile string) (output string, pass bool)) *ExecutionEngine {
	return &ExecutionEngine{
		registry: reg,
		gateway:  gw,
		mcpCall:  mcpCall,
		verify:   verify,
	}
}

// Execute 驱动完整的 ReAct 自主思考与工具调用闭环（统一单核）
func (e *ExecutionEngine) Execute(ctx context.Context, req *EngineRequest, eventChan chan<- EngineEvent) error {
	defer close(eventChan)

	if e == nil || e.registry == nil {
		eventChan <- EngineEvent{Type: EventError, ErrorMessage: "engine or registry is not initialized"}
		return fmt.Errorf("engine or registry is not initialized")
	}
	if req == nil {
		eventChan <- EngineEvent{Type: EventError, ErrorMessage: "engine request cannot be nil"}
		return fmt.Errorf("engine request cannot be nil")
	}

	return e.executeDirectLLM(ctx, req, eventChan)
}
