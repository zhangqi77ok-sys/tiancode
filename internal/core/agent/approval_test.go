package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"tiancode/internal/core/llm"
	"tiancode/internal/core/tools"
)

// countingTool 是计数用的假工具：审批拒绝时它必须**一次都不被调用**。
type countingTool struct {
	calls int
}

func (c *countingTool) Name() string            { return "shell" }
func (c *countingTool) Description() string     { return "假 shell" }
func (c *countingTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (c *countingTool) Execute(context.Context, json.RawMessage) (tools.ToolResult, error) {
	c.calls++
	return tools.ToolResult{Content: "ran"}, nil
}

type stubApprover struct {
	decision Decision
	err      error
	seen     []ApprovalRequest
}

func (s *stubApprover) Review(_ context.Context, req ApprovalRequest) (Decision, error) {
	s.seen = append(s.seen, req)
	return s.decision, s.err
}

func newApprovalLoop(t *testing.T) (*Loop, *countingTool) {
	t.Helper()
	tool := &countingTool{}
	registry := tools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	return &Loop{registry: registry}, tool
}

func callShell() llm.ToolCall {
	return llm.ToolCall{ID: "c1", Name: "shell", Arguments: `{"command":"rm -rf /tmp/x"}`}
}

// 默认关闭：未注入审批器时直接执行（行为与历史版本一致，零干扰）。
func TestExecTool_NoApproverRunsDirectly(t *testing.T) {
	l, tool := newApprovalLoop(t)
	if res := l.execToolWithApproval(context.Background(), callShell()); res.IsError {
		t.Fatalf("默认关时不应产生失败：%+v", res)
	}
	if tool.calls != 1 {
		t.Fatalf("calls = %d, want 1", tool.calls)
	}
}

// 允许：正常执行，且审批器看到的是原始工具名与参数（不做解析）。
func TestExecTool_Approved(t *testing.T) {
	l, tool := newApprovalLoop(t)
	ap := &stubApprover{decision: Decision{Approved: true}}
	l.SetApprover(ap)

	if res := l.execToolWithApproval(context.Background(), callShell()); res.IsError {
		t.Fatalf("批准后不应失败：%+v", res)
	}
	if tool.calls != 1 {
		t.Fatalf("calls = %d, want 1", tool.calls)
	}
	if len(ap.seen) != 1 || ap.seen[0].ToolName != "shell" || ap.seen[0].Arguments != `{"command":"rm -rf /tmp/x"}` {
		t.Fatalf("审批请求内容不符：%+v", ap.seen)
	}
}

// 拒绝：工具绝不执行，且失败结果对模型可见、含原因（ADR-0007 第 3 条）。
func TestExecTool_Denied(t *testing.T) {
	l, tool := newApprovalLoop(t)
	l.SetApprover(&stubApprover{decision: Decision{Approved: false, Reason: "危险命令"}})

	res := l.execToolWithApproval(context.Background(), callShell())
	if !res.IsError {
		t.Fatal("拒绝必须产生失败结果（不得静默拦截）")
	}
	if tool.calls != 0 {
		t.Fatalf("拒绝后工具仍被执行了 %d 次", tool.calls)
	}
	if !strings.Contains(res.Content, "危险命令") || !strings.Contains(res.Content, "shell") {
		t.Fatalf("失败结果应含原因与工具名：%q", res.Content)
	}
}

// 未给原因时补默认说明：模型不能收到空解释。
func TestExecTool_DeniedWithoutReason(t *testing.T) {
	l, _ := newApprovalLoop(t)
	l.SetApprover(&stubApprover{decision: Decision{Approved: false}})

	res := l.execToolWithApproval(context.Background(), callShell())
	if !res.IsError || !strings.Contains(res.Content, "用户未批准") {
		t.Fatalf("缺原因应补默认说明：%+v", res)
	}
}

// 审批通道故障 = 拒绝（故障时放行最危险）。
func TestExecTool_ApproverErrorDenies(t *testing.T) {
	l, tool := newApprovalLoop(t)
	l.SetApprover(&stubApprover{err: errors.New("UI 已断开")})

	res := l.execToolWithApproval(context.Background(), callShell())
	if !res.IsError || !strings.Contains(res.Content, "审批通道异常") {
		t.Fatalf("审批故障应拒绝并说明：%+v", res)
	}
	if tool.calls != 0 {
		t.Fatal("审批故障时绝不能执行工具")
	}
}

// 取消传播：ctx 取消时审批器应立即返回错误 → 视为拒绝（不挂起）。
func TestExecTool_CancelWhileWaiting(t *testing.T) {
	l, tool := newApprovalLoop(t)
	l.SetApprover(&stubApprover{err: context.Canceled})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := l.execToolWithApproval(ctx, callShell())
	if !res.IsError {
		t.Fatal("取消应视为拒绝")
	}
	if tool.calls != 0 {
		t.Fatal("取消后不得执行工具")
	}
}
