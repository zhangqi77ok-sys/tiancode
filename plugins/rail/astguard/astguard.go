// Package astguard 实现 DeepSeek Harness 的「AST 语义审查前置防护」：
// 在 fs 工具写盘前，对 .go 文件内容做 Go 语法预检，阻断非合法语法的落盘，
// 使"状态回溯安全网"之外再多一道"不让坏代码出生"的护栏。该能力经 Rail SPI 的
// OnBeforeAct 钩子接入执行内核，无需改动 loop 或 fs 工具本身。
package astguard

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"strings"

	v1 "tiancode/pkg/plugin/v1"
)

// Rail AST 语义审查护栏
type Rail struct{}

// New 构造 AST 审查护栏
func New() *Rail { return &Rail{} }

func (r *Rail) ID() string          { return "rail.astguard" }
func (r *Rail) Name() string        { return "ASTGuard" }
func (r *Rail) Version() string     { return "1.0.0" }
func (r *Rail) Type() v1.PluginType { return v1.TypeRail }
// 优先级低于 SafetyRail(100)，确保危险命令拦截先于语法审查
func (r *Rail) Priority() int { return 90 }

func (r *Rail) Init(context.Context, json.RawMessage) error { return nil }
func (r *Rail) Start(context.Context) error                 { return nil }
func (r *Rail) Stop(context.Context) error                  { return nil }
func (r *Rail) Health(context.Context) v1.HealthStatus {
	return v1.HealthStatus{Healthy: true, Message: "ASTGuard armed"}
}

func (r *Rail) OnBeforeObserve(context.Context, string) error { return nil }
func (r *Rail) OnBeforeReason(context.Context, string, *string) error { return nil }

// fsArgs 镜像 plugins/tool/fs 的写操作参数（仅取审查所需字段）
type fsArgs struct {
	Action   string `json:"action"`
	Path     string `json:"path"`
	RelPath  string `json:"rel_path"`
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

// OnBeforeAct 在算子调用前拦截：仅对 fs 的 write 动作、且目标为 .go 文件时做语法预检。
func (r *Rail) OnBeforeAct(_ context.Context, _ string, toolName string, args []byte) (*v1.RailDecision, error) {
	if toolName != "tool.fs" {
		return &v1.RailDecision{Allow: true}, nil
	}
	var a fsArgs
	if err := json.Unmarshal(args, &a); err != nil {
		// 参数解析失败不阻断，交由工具层自身报错
		return &v1.RailDecision{Allow: true}, nil
	}
	if strings.ToLower(strings.TrimSpace(a.Action)) != "write" {
		return &v1.RailDecision{Allow: true}, nil
	}
	target := strings.TrimSpace(a.Path)
	if target == "" {
		target = strings.TrimSpace(a.RelPath)
	}
	if target == "" {
		target = strings.TrimSpace(a.FilePath)
	}
	if !strings.HasSuffix(strings.ToLower(target), ".go") {
		return &v1.RailDecision{Allow: true}, nil
	}
	if strings.TrimSpace(a.Content) == "" {
		return &v1.RailDecision{Allow: true}, nil
	}
	if _, err := parser.ParseFile(token.NewFileSet(), target, a.Content, parser.AllErrors); err != nil {
		return &v1.RailDecision{
			Allow:       false,
			Intercepted: true,
			Reason:      fmt.Sprintf("AST 语义审查拦截：%s 写入内容非合法 Go 语法: %v", target, err),
		}, nil
	}
	return &v1.RailDecision{Allow: true}, nil
}

func (r *Rail) OnAfterAct(context.Context, string, string, *v1.ToolResult) error { return nil }
func (r *Rail) OnVerify(context.Context, string) (bool, string, error) {
	return true, "", nil
}
