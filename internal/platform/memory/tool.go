// memory 工具：模型读写两级长期记忆的唯一入口。
//
// 为什么做成工具而不是只在系统提示里注入：记忆的"写"必须由模型在对话中自主完成
// （用户说"记住我用 pnpm"→ 模型当场落一条），"读"则由每轮注入兜底（见 app 层
// memorySection——模型不调用工具也能看到记忆）。工具只负责写与显式读。
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tiancode/internal/core/tools"
)

// opTimeout 是单次记忆操作的时限：本地小文件读写本应瞬时完成，但工具执行契约
// 要求有界（R3）——文件系统卡死（网络盘/杀软扫描）时到期给模型一个确定错误，
// 而不是永久挂住整轮。
const opTimeout = 5 * time.Second

// Tool 是记忆工具。root 是这场对话的工作区根（workspace scope 的落点）；
// 空 = 纯对话会话：workspace scope 显式报错，global scope 照常可用。
type Tool struct {
	store *Store
	root  string
}

// NewTool 构造记忆工具（共享工具：不碰工作区文件，root 只决定 workspace scope 的存储键）。
func NewTool(store *Store, root string) *Tool {
	return &Tool{store: store, root: root}
}

// Name 实现工具端口。
func (t *Tool) Name() string { return "memory" }

// Description 实现工具端口。
func (t *Tool) Description() string {
	return "读写长期记忆（跨对话留存）。用户说出偏好、约定、项目背景等值得长期记住的事实时主动保存一条；" +
		"记忆每轮对话自动注入你的系统提示，无需重复询问。scope=global 存用户个人偏好（跨项目），" +
		"scope=workspace 存当前项目的约定。禁止存密钥、临时状态与一次性的任务细节。"
}

// Schema 实现工具端口。
func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["append", "delete", "read"], "description": "append=追加一条；delete=按行号删除；read=读取全部"},
    "scope": {"type": "string", "enum": ["global", "workspace"], "description": "global=用户偏好（跨项目）；workspace=当前项目约定（无工作区的纯对话不可用）"},
    "text": {"type": "string", "description": "action=append 时必填：一条简短事实（一句话，如「用户偏好 pnpm，不用 npm」）"},
    "line": {"type": "integer", "description": "action=delete 时必填：要删除的行号（从 1 开始，read 可见）"}
  },
  "required": ["action", "scope"]
}`)
}

// Execute 实现工具端口：业务失败走 IsError（模型可见、可继续推理）。
func (t *Tool) Execute(ctx context.Context, args json.RawMessage) (tools.ToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	var p struct {
		Action string `json:"action"`
		Scope  string `json:"scope"`
		Text   string `json:"text"`
		Line   int    `json:"line"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return tools.ToolResult{Content: "参数解析失败：" + err.Error(), IsError: true}, nil
	}
	scope := Scope(p.Scope)
	if scope != ScopeGlobal && scope != ScopeWorkspace {
		return tools.ToolResult{Content: fmt.Sprintf("scope 只允许 global / workspace，收到 %q", p.Scope), IsError: true}, nil
	}
	if err := ctx.Err(); err != nil {
		return tools.ToolResult{Content: "记忆操作超时：" + err.Error(), IsError: true, TimedOut: true}, nil
	}
	switch strings.TrimSpace(p.Action) {
	case "read":
		lines, err := t.store.Lines(scope, t.root)
		if err != nil {
			return tools.ToolResult{Content: err.Error(), IsError: true}, nil
		}
		if len(lines) == 0 {
			return tools.ToolResult{Content: "（该作用域还没有记忆）"}, nil
		}
		var b strings.Builder
		for i, l := range lines {
			fmt.Fprintf(&b, "%d. %s\n", i+1, l)
		}
		return tools.ToolResult{Content: strings.TrimRight(b.String(), "\n")}, nil
	case "append":
		if strings.TrimSpace(p.Text) == "" {
			return tools.ToolResult{Content: "append 需要 text（一条简短事实）", IsError: true}, nil
		}
		if err := t.store.Append(scope, t.root, p.Text); err != nil {
			return tools.ToolResult{Content: err.Error(), IsError: true}, nil
		}
		return tools.ToolResult{Content: "已记住：" + strings.TrimSpace(p.Text), Op: "write"}, nil
	case "delete":
		if p.Line < 1 {
			return tools.ToolResult{Content: "delete 需要 line（从 1 开始，先 read 确认行号）", IsError: true}, nil
		}
		if err := t.store.Delete(scope, t.root, p.Line); err != nil {
			return tools.ToolResult{Content: err.Error(), IsError: true}, nil
		}
		return tools.ToolResult{Content: fmt.Sprintf("已删除第 %d 条", p.Line)}, nil
	default:
		return tools.ToolResult{Content: fmt.Sprintf("action 只允许 read / append / delete，收到 %q", p.Action), IsError: true}, nil
	}
}
