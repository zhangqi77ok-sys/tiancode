package arch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"tiancode/internal/ast"
	v1 "tiancode/pkg/plugin/v1"
)

// Tool Go AST 与架构拓扑分析算子插件
type Tool struct {
	id        string
	name      string
	version   string
	workspace string
}

// NewTool 构造实例
func NewTool(workspace string) *Tool {
	return &Tool{
		id:        "tool.arch",
		name:      "Go AST & Architecture Analysis Tool",
		version:   "1.0.0",
		workspace: workspace,
	}
}

func (t *Tool) ID() string                          { return t.id }
func (t *Tool) Name() string                        { return t.name }
func (t *Tool) Version() string                     { return t.version }
func (t *Tool) Type() v1.PluginType                 { return v1.TypeTool }
func (t *Tool) Init(ctx context.Context, cfg json.RawMessage) error { return nil }
func (t *Tool) Start(ctx context.Context) error     { return nil }
func (t *Tool) Stop(ctx context.Context) error      { return nil }
func (t *Tool) Health(ctx context.Context) v1.HealthStatus {
	return v1.HealthStatus{Healthy: true, Message: "AST Architecture Tool ready"}
}

func (t *Tool) Definition() v1.ToolDefinition {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"inspect", "blast_radius", "discover_modules"},
				"description": "分析动作: inspect (扫描代码库架构分层拓扑、契约实现与依赖违规检测), blast_radius (针对指定符号/类型分析改动影响面与具体调用点行号), discover_modules (发现 Monorepo 模块与子应用)",
			},
			"target": map[string]any{
				"type":        "string",
				"description": "分析目标符号或类型名称 (action=blast_radius 时必填，如 'pkg/plugin/v1.ToolPlugin' 或 'loop.Engine')",
			},
			"root_dir": map[string]any{
				"type":        "string",
				"description": "目标扫描目录绝对或相对路径 (可选，默认使用当前项目工作区根目录)",
			},
		},
		"required": []string{"action"},
	}
	schemaBytes, _ := json.Marshal(schema)

	return v1.ToolDefinition{
		Name:        "code_architecture",
		Description: "深入解析 Go AST 语法树、架构分层依赖 DAG、契约多态匹配与符号级改动影响面。在重构或修改核心代码前必须调用此工具评估波及范围与合规性。",
		Parameters:  schemaBytes,
		Mutating:    false,
	}
}

func (t *Tool) Execute(ctx context.Context, rawArgs json.RawMessage) (*v1.ToolResult, error) {
	var args struct {
		Action  string `json:"action"`
		Target  string `json:"target"`
		RootDir string `json:"root_dir"`
	}

	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return &v1.ToolResult{Content: fmt.Sprintf("invalid arguments: %v", err), IsError: true}, nil
	}

	targetDir := strings.TrimSpace(args.RootDir)
	if targetDir == "" {
		targetDir = t.workspace
	}
	if targetDir == "" {
		return &v1.ToolResult{Content: "error: workspace root directory is empty", IsError: true}, nil
	}

	action := strings.ToLower(strings.TrimSpace(args.Action))
	switch action {
	case "inspect":
		report, err := ast.AnalyzeWorkspaceArchitecture(targetDir)
		if err != nil {
			return &v1.ToolResult{Content: fmt.Sprintf("failed to analyze architecture: %v", err), IsError: true}, nil
		}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return &v1.ToolResult{Content: fmt.Sprintf("failed to serialize report: %v", err), IsError: true}, nil
		}
		return &v1.ToolResult{Content: string(data), IsError: false}, nil

	case "blast_radius":
		target := strings.TrimSpace(args.Target)
		if target == "" {
			return &v1.ToolResult{Content: "error: 'target' is required for blast_radius action (e.g., 'pkg/plugin/v1.ToolPlugin')", IsError: true}, nil
		}
		blast, err := ast.AnalyzeBlastRadius(targetDir, target)
		if err != nil {
			return &v1.ToolResult{Content: fmt.Sprintf("failed to analyze blast radius: %v", err), IsError: true}, nil
		}
		data, err := json.MarshalIndent(blast, "", "  ")
		if err != nil {
			return &v1.ToolResult{Content: fmt.Sprintf("failed to serialize blast report: %v", err), IsError: true}, nil
		}
		return &v1.ToolResult{Content: string(data), IsError: false}, nil

	case "discover_modules":
		modules, err := ast.DiscoverGoModules(targetDir)
		if err != nil {
			return &v1.ToolResult{Content: fmt.Sprintf("failed to discover modules: %v", err), IsError: true}, nil
		}
		data, err := json.MarshalIndent(modules, "", "  ")
		if err != nil {
			return &v1.ToolResult{Content: fmt.Sprintf("failed to serialize modules: %v", err), IsError: true}, nil
		}
		return &v1.ToolResult{Content: string(data), IsError: false}, nil

	default:
		return &v1.ToolResult{
			Content: fmt.Sprintf("unknown action '%s', supported actions: 'inspect', 'blast_radius', 'discover_modules'", args.Action),
			IsError: true,
		}, nil
	}
}
