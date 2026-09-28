package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// todo 工具：模型用它维护本轮任务清单（全量快照，非增量）。
// 为什么由 Loop 按名拦截而不走普通工具执行：清单要实时推 UI（TodoEvent）
// 并落账本（EventTodo 供 Replay 恢复），普通工具执行路径拿不到账本与事件通道。
const todoToolName = "todo"

// TodoTool 是 todo 的端口实现：只向模型提供定义；Execute 是防御路径
// （Loop 按名拦截，正常不会走到）。
type TodoTool struct{}

func NewTodoTool() *TodoTool { return &TodoTool{} }

func (t *TodoTool) Name() string { return todoToolName }

func (t *TodoTool) Description() string {
	return "维护本轮任务清单（全量快照）：规划多步任务时先列出全部条目，此后每开始/完成一项就重新提交整张清单"
}

func (t *TodoTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "items": {
      "type": "array",
      "description": "完整清单（每次全量提交）",
      "items": {
        "type": "object",
        "properties": {
          "text": {"type": "string", "description": "任务条目"},
          "status": {"type": "string", "enum": ["pending", "in_progress", "done"]}
        },
        "required": ["text", "status"]
      }
    }
  },
  "required": ["items"]
}`)
}

func (t *TodoTool) Execute(_ context.Context, _ json.RawMessage) (tools.ToolResult, error) {
	return tools.ToolResult{Content: "todo is handled by the agent loop", IsError: true}, nil
}

// todoItems 解析并校验 todo 工具参数（模型可见的业务失败走 IsError）。
func todoItems(raw string) ([]llm.TodoItem, error) {
	var a struct {
		Items []llm.TodoItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, fmt.Errorf("invalid arguments: %v", err)
	}
	if len(a.Items) == 0 {
		return nil, fmt.Errorf("items is required (at least one entry)")
	}
	for i, it := range a.Items {
		if strings.TrimSpace(it.Text) == "" {
			return nil, fmt.Errorf("items[%d].text is required", i)
		}
		switch it.Status {
		case "pending", "in_progress", "done":
		default:
			return nil, fmt.Errorf("items[%d].status must be pending|in_progress|done", i)
		}
	}
	return a.Items, nil
}

// runTodo 处理 todo 调用：校验 → 落账 EventTodo → 实时推 TodoEvent →
// 返回给模型的确认结果（带进度计数）。所有失败都以 IsError 结果回模型，不中断轮次。
func runTodo(call llm.ToolCall, ledger *session.Ledger, forward func(llm.StreamChunk) bool) tools.ToolResult {
	const title, op = "任务清单", "todo"
	items, err := todoItems(call.Arguments)
	if err != nil {
		return tools.ToolResult{Content: err.Error(), IsError: true, Title: title, Op: op}
	}
	if _, err := ledger.Append(session.EventTodo, map[string]any{"items": items}); err != nil {
		return tools.ToolResult{Content: fmt.Sprintf("persist todo: %v", err), IsError: true, Title: title, Op: op}
	}
	forward(llm.StreamChunk{Todo: &llm.TodoEvent{Items: items}})
	done := 0
	for _, it := range items {
		if it.Status == "done" {
			done++
		}
	}
	return tools.ToolResult{
		Content: fmt.Sprintf("todo list updated: %d/%d done", done, len(items)),
		Title:   title,
		Op:      op,
	}
}
