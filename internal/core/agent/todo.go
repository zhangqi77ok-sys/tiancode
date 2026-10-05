package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
          "status": {"type": "string", "enum": ["pending", "in_progress", "done"]},
          "files": {
            "type": "array",
            "description": "这一项预期改动的文件（工作区相对路径）。声明后系统会核对本轮是否真的写入过——声明了却没动会被拒绝，所以只在确有把握时填；拿不准就留空（留空=不核对）。",
            "items": {"type": "string"}
          }
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
		// files 声明校验（档位 3）：空白与归一后重复的路径显式拒绝——
		// 模型写错要让它知道，不静默丢弃（静默会让核账口径与模型认知脱节）。
		seen := map[string]bool{}
		for j, f := range it.Files {
			f = strings.TrimSpace(f)
			if f == "" {
				return nil, fmt.Errorf("items[%d].files[%d] must not be blank", i, j)
			}
			key := normalizePathKey(f)
			if seen[key] {
				return nil, fmt.Errorf("items[%d].files has duplicate path %q", i, f)
			}
			seen[key] = true
		}
	}
	return a.Items, nil
}

// normalizePathKey 归一文件路径用于比对：Windows 大小写不敏感、斜杠等价、
// 去掉冗余分隔。核账两侧（模型声明 / 实际写入）都过这个函数，口径才一致。
func normalizePathKey(p string) string {
	s := strings.ReplaceAll(strings.TrimSpace(p), `/`, `\`)
	s = strings.TrimPrefix(s, `.\`)
	return strings.ToLower(filepath.Clean(s))
}

// writeTargetOf 判断一次工具调用是否为"写入了某个文件"，返回归一化路径。
// 白名单口径与 isReadOnlyCall 同一纪律：只认 fs 的 write/replace，
// 其余（shell 重定向、MCP 写操作、解析不出的形态）一律不认定——
// 核账宁可漏判也不误判，否则会拿"模型声明得对、系统没认出来"去拒绝模型。
func writeTargetOf(call llm.ToolCall) (string, bool) {
	if call.Name != "fs" {
		return ``, false
	}
	var p struct {
		Action string `json:"action"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &p); err != nil {
		return ``, false
	}
	if p.Action != "write" && p.Action != "replace" {
		return ``, false
	}
	path := strings.TrimSpace(p.Path)
	if path == "" {
		return ``, false
	}
	return normalizePathKey(path), true
}

// unaudited 返回"标 done、声明了 files、但本轮没观察到写入"的条目描述。
// 三类降级放行：未声明 files（旧账本/模型没把握）、非 done、声明文件全部写过。
func (l *Loop) unaudited(items []llm.TodoItem) []string {
	var bad []string
	for _, it := range items {
		if it.Status != "done" || len(it.Files) == 0 {
			continue
		}
		for _, f := range it.Files {
			if !l.written[normalizePathKey(f)] {
				bad = append(bad, fmt.Sprintf("%q（声明改 %s）", it.Text, f))
				break
			}
		}
	}
	return bad
}

// runTodo 处理 todo 调用：校验 → 落账 EventTodo → 实时推 TodoEvent → 记下快照 →
// 返回给模型的确认结果（带进度计数）。所有失败都以 IsError 结果回模型，不中断轮次。
// 挂在 Loop 上是为了记快照（阶段 4-1 的过期提醒判据）——todo 仍由 Loop 按名拦截。
func (l *Loop) runTodo(call llm.ToolCall, ledger *session.Ledger, forward func(llm.StreamChunk) bool) tools.ToolResult {
	const title, op = "任务清单", "todo"
	items, err := todoItems(call.Arguments)
	if err != nil {
		return tools.ToolResult{Content: err.Error(), IsError: true, Title: title, Op: op}
	}
	// 客观核账（档位 3）：声明 done 但本轮没观察到对所声明文件的写入 → 拒绝。
	// 判据只来自系统侧的写入记录（l.written），不信模型自报。模型可以重交一次
	// 绕过——核账的价值是让漏项当场暴露一次，不是杜绝漏项（设计文档"边界"一节）。
	if bad := l.unaudited(items); len(bad) > 0 {
		return tools.ToolResult{
			Content: fmt.Sprintf("todo 核账未通过，以下条目标为 done 但本轮未观察到对所声明文件的写入：%s。"+
				"要么现在真的改掉，要么把状态改回 in_progress/pending；"+
				"若这些文件已由用户在本会话外完成，请在重交清单时说明。",
				strings.Join(bad, "；")),
			IsError: true, Title: title, Op: op,
		}
	}
	if _, err := ledger.Append(session.EventTodo, map[string]any{"items": items}); err != nil {
		return tools.ToolResult{Content: fmt.Sprintf("persist todo: %v", err), IsError: true, Title: title, Op: op}
	}
	l.todo.observe(items) // 进度已回写：清掉"该催重交"的状态
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
