package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/tools"
)

// AskRequest 是一次"模型向用户提问"请求：问题 + 选项（可空 = 自由回答）。
type AskRequest struct {
	Question string
	Options  []string
}

// Asker 是问答端口：UI 渲染选项卡，用户答复原样回流为工具结果。
// 与审批端口（ADR-0007）同构：内核只负责"问"与"等待"，决策权完全在用户。
type Asker interface {
	Ask(ctx context.Context, req AskRequest) (string, error)
}

// SetAsker 注入问答器；nil 表示问答通道未启用（ask_user 收到引导性结果而非错误，
// 避免模型在无 UI 环境反复重试提问）。
func (l *Loop) SetAsker(a Asker) { l.asker = a }

// ask_user 工具：模型用它在多方案间征求用户选择（问答式人机交互）。
// 与 todo 同理由 Loop 按名拦截：要阻塞等 UI 答复并产生交互事件，普通工具路径做不到。
const askToolName = "ask_user"

// AskUserTool 是 ask_user 的端口实现：只向模型提供定义；Execute 是防御路径。
type AskUserTool struct{}

func NewAskUserTool() *AskUserTool { return &AskUserTool{} }

func (t *AskUserTool) Name() string { return askToolName }

func (t *AskUserTool) Description() string {
	return "向用户提出选择题以继续任务：在多个可行方案、关键取舍或缺少必要信息时使用；问题应简明，选项 2~4 个、每个一句话"
}

func (t *AskUserTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "question": {"type": "string", "description": "要问用户的问题（一句话）"},
    "options": {
      "type": "array",
      "description": "候选项（2~4 个，每项一句话；留空表示自由回答）",
      "items": {"type": "string"}
    }
  },
  "required": ["question"]
}`)
}

func (t *AskUserTool) Execute(_ context.Context, _ json.RawMessage) (tools.ToolResult, error) {
	return tools.ToolResult{Content: "ask_user is handled by the agent loop", IsError: true}, nil
}

// parseAsk 校验 ask_user 参数。
func parseAsk(raw string) (AskRequest, error) {
	var a struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return AskRequest{}, fmt.Errorf("invalid arguments: %v", err)
	}
	if strings.TrimSpace(a.Question) == "" {
		return AskRequest{}, fmt.Errorf("question is required")
	}
	cleaned := make([]string, 0, len(a.Options))
	for _, o := range a.Options {
		if o = strings.TrimSpace(o); o != "" {
			cleaned = append(cleaned, o)
		}
	}
	return AskRequest{Question: a.Question, Options: cleaned}, nil
}

// continueAfterLimit 询问用户是否续跑下一段（第 3 批）。
// 语义：经 ask_user 同款通道发一条可取消的询问；用户明确选择"继续"返回 true。
// 拒绝/取消/无问答通道一律 false（调用方据此走收尾；取消由 ctx 收束）。
func (l *Loop) continueAfterLimit(ctx context.Context, segment int) bool {
	if l.asker == nil {
		return false // 无问答通道（纯 API/测试）：保持旧的"总结收尾"行为
	}
	answer, err := l.asker.Ask(ctx, AskRequest{
		Question: fmt.Sprintf("已连续执行 %d 步（第 %d 段用尽），是否继续执行下一段（再 %d 步）？",
			segment*MaxStepsPerTurn, segment, MaxStepsPerTurn),
		Options: []string{"继续执行", "就此结束"},
	})
	if err != nil {
		return false
	}
	// 只有明确包含"继续"才续跑：自由回答里的其他内容一律按结束处理（保守）。
	return strings.Contains(strings.TrimSpace(answer), "继续")
}

// runAsk 处理 ask_user 调用：校验 → 经问答端口阻塞等 UI 答复 → 答案作为工具结果回模型。
// 纪律与审批一致：取消/超时是模型可见的失败（可据此收尾），绝不静默。
func (l *Loop) runAsk(ctx context.Context, call llm.ToolCall) tools.ToolResult {
	const title, op = "问答", "ask"
	req, err := parseAsk(call.Arguments)
	if err != nil {
		return tools.ToolResult{Content: err.Error(), IsError: true, Title: title, Op: op}
	}
	if l.asker == nil {
		// 问答通道未启用：引导模型自行决策，而不是报错诱发重试
		return tools.ToolResult{
			Content: "ask_user channel is unavailable; proceed with your best judgment and state your assumption",
			Title:   title, Op: op,
		}
	}
	answer, err := l.asker.Ask(ctx, req)
	if err != nil {
		return tools.ToolResult{
			Content: fmt.Sprintf("用户未作答（%v）：请基于现有信息继续，并说明你的假设", err),
			IsError: true,
			Title:   title, Op: op,
		}
	}
	return tools.ToolResult{Content: answer, Title: title, Op: op}
}
