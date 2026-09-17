package ollama

import (
	"context"
	"encoding/json"
	"time"

	v1 "tiancode/pkg/plugin/v1"
	"tiancode/plugins/provider/openai"
)

type Provider struct {
	baseProvider *openai.Provider
}

func NewProvider() *Provider {
	return &Provider{
		baseProvider: openai.NewProvider(),
	}
}

func (p *Provider) ID() string {
	return "provider.ollama"
}

func (p *Provider) Name() string {
	return "ollama"
}

func (p *Provider) Version() string {
	return "1.0.0"
}

func (p *Provider) Type() v1.PluginType {
	return v1.TypeProvider
}

func (p *Provider) Init(ctx context.Context, configBytes json.RawMessage) error {
	var config map[string]interface{}
	json.Unmarshal(configBytes, &config)
	url, _ := config["base_url"].(string)
	if url == "" {
		url = "http://localhost:11434/v1"
	}
	config["base_url"] = url
	config["api_key"] = "ollama"
	newBytes, _ := json.Marshal(config)
	return p.baseProvider.Init(ctx, newBytes)
}

func (p *Provider) Start(ctx context.Context) error {
	return p.baseProvider.Start(ctx)
}

func (p *Provider) Stop(ctx context.Context) error {
	return p.baseProvider.Stop(ctx)
}

func (p *Provider) Health(ctx context.Context) v1.HealthStatus {
	return p.baseProvider.Health(ctx)
}

func (p *Provider) StreamChat(ctx context.Context, req *v1.ChatRequest) (<-chan v1.StreamChunk, error) {
	return p.baseProvider.StreamChat(ctx, req)
}

func (p *Provider) Ping(ctx context.Context) (time.Duration, error) {
	return p.baseProvider.Ping(ctx)
}

func (p *Provider) ListModels(ctx context.Context) ([]v1.ModelDescriptor, error) {
	return []v1.ModelDescriptor{
		{ID: "llama3", Name: "Llama 3"},
		{ID: "qwen2", Name: "Qwen 2"},
	}, nil
}