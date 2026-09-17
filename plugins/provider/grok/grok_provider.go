package grok

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
	return "provider.grok"
}

func (p *Provider) Name() string {
	return "grok"
}

func (p *Provider) Version() string {
	return "1.0.0"
}

func (p *Provider) Type() v1.PluginType {
	return v1.TypeProvider
}

func (p *Provider) Init(ctx context.Context, configBytes json.RawMessage) error {
	var config map[string]interface{}
	_ = json.Unmarshal(configBytes, &config)
	if config == nil {
		config = make(map[string]interface{})
	}
	url, _ := config["base_url"].(string)
	if url == "" {
		url = "https://api.x.ai/v1"
	}
	config["base_url"] = url
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
	models, err := p.baseProvider.ListModels(ctx)
	if err == nil && len(models) > 0 {
		return models, nil
	}
	return []v1.ModelDescriptor{
		{ID: "grok-4.6", Name: "Grok 4.6", Provider: "grok", ContextWindow: 131072, SupportThinking: true},
		{ID: "grok-2-1212", Name: "Grok 2 (1212)", Provider: "grok", ContextWindow: 131072},
		{ID: "grok-2-vision-1212", Name: "Grok 2 Vision", Provider: "grok", ContextWindow: 32768},
		{ID: "grok-beta", Name: "Grok Beta", Provider: "grok", ContextWindow: 131072},
	}, nil
}
