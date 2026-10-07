// Package subagenttool 实现 task 工具：派生**只读子代理**。
//
// 做什么：把一段独立的调研任务交给一个临时的 ReAct 循环执行——子代理有自己的
// 一次性账本与只读工具集（fs 读取类 / search / git / webfetch / memory 读），
// 跑完后把最终报告作为工具结果交还主对话。
// 为什么需要：主对话的上下文是稀缺资源。探索（grep、读文件、翻代码）产生的
// 大量中间输出都留在主上下文里，任务一大模型就开始"忘事"；子代理在自己的
// 上下文里消化这些中间输出，只把结论带回来。
//
// 边界铁律：
//   - 只读：写路径在工具包装层结构性排除（readOnlyTool），子代理改不了任何文件；
//   - 不递归：子代理工具集里没有 task 自己；
//   - 隔离：子代理账本建在临时目录，跑完即删，绝不污染主会话与侧栏；
//   - 取消传播：主轮被中断时 ctx 取消，子代理随终态收束。
package subagenttool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"tiancode/internal/core/agent"
	"tiancode/internal/core/llm"
	"tiancode/internal/core/session"
	"tiancode/internal/core/tools"
)

// taskTimeout 是单个子代理任务的硬上限（C-TOOL-1：工具必须自带超时）。
// 调研任务合法地比普通工具慢（几十步工具调用），但不能没有刹车。
const taskTimeout = 15 * time.Minute

// resultLimit 是交还主对话的报告字节上限（头尾保留）：报告要进主对话上下文，
// 必须有界——主上下文才是要保护的对象。
const resultLimit = 16 << 10

// subPreface 是子代理的系统说明。
const subPreface = "你是一个只读调研子代理。你的最终回复是唯一的交付物，会被原样交还给主对话，" +
	"所以它必须自包含：写清结论、依据（文件路径:行号）、未解决的疑问与建议的下一步。" +
	"你只有只读工具：不能也不需要修改任何文件、执行任何写操作。" +
	"不要寒暄、不要复述任务，直接给出结构化的调研报告（中文）。"

// Tool 是 task 工具：构造期注入运行时、模型名与主装配挑出的只读工具实例。
type Tool struct {
	runtime  llm.ChatRuntime
	model    string
	readonly []tools.ToolPort // 主装配的只读工具实例（fs/search/git/webfetch/memory）
}

// New 构造 task 工具。readonly 里出现的每个工具都会再包一层只读闸门
// （fs 只放行读取类 action，memory 只放行 read）——不信任主装配的意图。
func New(rt llm.ChatRuntime, model string, readonly ...tools.ToolPort) *Tool {
	return &Tool{runtime: rt, model: model, readonly: readonly}
}

func (t *Tool) Name() string { return "task" }

func (t *Tool) Description() string {
	return "派生一个只读子代理去执行独立的调研任务（翻代码、搜索、读文档），只把最终报告带回来。" +
		"适合：大范围探索、多文件调研、需要大量中间读取但只要结论的工作。" +
		"子代理看不到本对话的任何历史，prompt 必须自包含（背景、目标、路径、期望产出）。" +
		"不能用它修改文件或执行命令——子代理只有只读工具。"
}

func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "prompt": {"type": "string", "description": "交给子代理的完整任务说明：背景、要弄清的问题、相关路径、期望的报告内容。必须自包含（子代理看不到本对话）。"},
    "expected": {"type": "string", "description": "期望的报告形态（可选），如：列出调用链与关键函数签名；或：对比两种方案的结论。"}
  },
  "required": ["prompt"]
}`)
}

// Execute 派生子代理并同步等待其报告（C-TOOL-1：自带超时；取消经 ctx 传播）。
func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tools.ToolResult, error) {
	var p struct {
		Prompt   string `json:"prompt"`
		Expected string `json:"expected"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return tools.ToolResult{Content: "task 参数不是合法 JSON：" + err.Error(), IsError: true}, nil
	}
	prompt := strings.TrimSpace(p.Prompt)
	if prompt == "" {
		return tools.ToolResult{Content: "task 需要 prompt：告诉子代理要调研什么（它看不到本对话历史）。", IsError: true}, nil
	}
	if strings.TrimSpace(p.Expected) != "" {
		prompt += "\n\n期望的报告形态：" + strings.TrimSpace(p.Expected)
	}
	ctx, cancel := context.WithTimeout(ctx, taskTimeout)
	defer cancel()

	registry, err := t.buildRegistry()
	if err != nil {
		return tools.ToolResult{Content: "子代理装配失败：" + err.Error(), IsError: true}, nil
	}
	// 一次性账本：临时目录里建、跑完删（C-SUB-1 隔离）。删除失败不吞——
	// 留着临时文件比静默泄漏好，错误并入报告不影响主轮。
	dir, err := os.MkdirTemp("", "tiancode-task-")
	if err != nil {
		return tools.ToolResult{Content: "子代理账本创建失败：" + err.Error(), IsError: true}, nil
	}
	defer func() {
		if rerr := os.RemoveAll(dir); rerr != nil {
			fmt.Fprintf(os.Stderr, "subagent ledger cleanup: %v\n", rerr)
		}
	}()
	ledger, err := session.OpenLedger(dir, "task")
	if err != nil {
		return tools.ToolResult{Content: "子代理账本打开失败：" + err.Error(), IsError: true}, nil
	}
	defer ledger.Close()

	loop := agent.NewLoop(t.runtime, t.model, registry)
	loop.SetPreface(subPreface)
	ch, err := loop.Run(ctx, ledger, prompt)
	if err != nil {
		return tools.ToolResult{Content: "子代理启动失败：" + err.Error(), IsError: true}, nil
	}
	var report strings.Builder
	var terminal llm.StreamChunk
	for c := range ch {
		if c.EndReason != llm.EndNone {
			terminal = c
			break
		}
		if c.Delta != "" {
			report.WriteString(c.Delta)
		}
	}
	switch terminal.EndReason {
	case llm.EndDone:
		content := strings.TrimSpace(report.String())
		if content == "" {
			content = "子代理没有产出任何报告（可能没找到相关信息）。"
		}
		return tools.ToolResult{
			Content: tools.HeadTail(content, resultLimit),
			Title:   "子代理调研",
			Op:      "subagent",
		}, nil
	case llm.EndCancelled:
		return tools.ToolResult{Content: "子代理任务被中断。", IsError: true, TimedOut: true}, nil
	default:
		reason := "未知"
		if terminal.Err != nil {
			reason = terminal.Err.Error()
		}
		return tools.ToolResult{Content: "子代理执行失败（" + reason + "）。", IsError: true, TimedOut: terminal.EndReason == llm.EndIdleTimeout}, nil
	}
}

// buildRegistry 组装子代理的只读工具集：主装配的只读实例逐个包上闸门。
// 没有 task、没有写工具——递归与写路径都不在场（结构性排除，不靠自觉）。
func (t *Tool) buildRegistry() (*tools.Registry, error) {
	registry := tools.NewRegistry()
	for _, inner := range t.readonly {
		if inner == nil {
			continue
		}
		if err := registry.Register(readOnlyTool{inner: inner}); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// readOnlyTool 是只读闸门：按工具名与 action 白名单放行，其余一律拒绝。
// 白名单与内核 isReadOnlyCall 的只读口径一致（fs 读取类 / memory read）；
// search/git/webfetch 整体只读，原样放行（闸门统一包一层便于审计）。
type readOnlyTool struct {
	inner tools.ToolPort
}

func (g readOnlyTool) Name() string        { return g.inner.Name() }
func (g readOnlyTool) Description() string { return g.inner.Description() }
func (g readOnlyTool) Schema() json.RawMessage {
	return g.inner.Schema()
}

func (g readOnlyTool) Execute(ctx context.Context, args json.RawMessage) (tools.ToolResult, error) {
	var p struct {
		Action string `json:"action"`
	}
	// 解析失败按"action 不可判定"处理：fs/memory 拒绝（宁可错杀），其余照旧放行。
	actionKnown := json.Unmarshal(args, &p) == nil
	switch g.inner.Name() {
	case "fs":
		if !actionKnown {
			return tools.ToolResult{Content: "子代理只读：fs 参数解析失败，调用被拒绝。", IsError: true}, nil
		}
		switch p.Action {
		case "read", "list", "tree", "symbols":
		default:
			return tools.ToolResult{Content: "子代理只读：" + g.inner.Name() + " 的 " + p.Action + " 被拒绝（这是主对话的工具纪律，不是任务本身失败）。", IsError: true}, nil
		}
	case "memory":
		if !actionKnown || p.Action != "read" {
			return tools.ToolResult{Content: "子代理只读：memory 的 " + p.Action + " 被拒绝。", IsError: true}, nil
		}
	}
	return g.inner.Execute(ctx, args)
}
