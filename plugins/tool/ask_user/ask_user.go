package ask_user

import (
	"context"
	"encoding/json"

	v1 "tiancode/pkg/plugin/v1"
)

type Tool struct{}

func NewTool() *Tool {
	return &Tool{}
}

func (t *Tool) ID() string      { return "tool.ask_user" }
func (t *Tool) Name() string    { return "ask_user" }
func (t *Tool) Version() string { return "1.0.0" }
func (t *Tool) Type() v1.PluginType { return v1.TypeTool }
func (t *Tool) Init(ctx context.Context, config json.RawMessage) error { return nil }
func (t *Tool) Start(ctx context.Context) error { return nil }
func (t *Tool) Stop(ctx context.Context) error  { return nil }
func (t *Tool) Health(ctx context.Context) v1.HealthStatus { return v1.HealthStatus{Healthy: true} }

func (t *Tool) Definition() v1.ToolDefinition {
	return v1.ToolDefinition{
		Name:        "ask_user",
		Description: "遇到互斥实现方案时必须调用 ask_user 询问用户，禁止擅自选一个继续改盘。",
		Parameters: []byte(`{
			"type": "object",
			"properties": {
				"question": {
					"type": "string",
					"description": "展示给用户的提问标题"
				},
				"options": {
					"type": "array",
					"description": "2~5个互斥的选项",
					"items": {
						"type": "object",
						"properties": {
							"id": { "type": "string" },
							"label": { "type": "string" },
							"description": { "type": "string" },
							"recommended": { "type": "boolean" }
						},
						"required": ["id", "label"]
					}
				},
				"allow_custom": {
					"type": "boolean",
					"description": "是否允许补充输入，默认 true"
				}
			},
			"required": ["question", "options"]
		}`),
		Mutating: false,
	}
}

func (t *Tool) Execute(ctx context.Context, args json.RawMessage) (*v1.ToolResult, error) {
	return &v1.ToolResult{
		Content: "ask_user should be intercepted by engine",
	}, nil
}
