package azure

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	v1 "tiancode/pkg/plugin/v1"
)

type Provider struct {
	apiKey     string
	baseURL    string
	apiVersion string
}

func NewProvider() *Provider {
	return &Provider{
		apiVersion: "2024-02-15-preview",
	}
}

func (p *Provider) ID() string {
	return "provider.azure"
}

func (p *Provider) Name() string {
	return "azure"
}

func (p *Provider) Version() string {
	return "1.0.0"
}

func (p *Provider) Type() v1.PluginType {
	return v1.TypeProvider
}

func (p *Provider) Init(ctx context.Context, configBytes json.RawMessage) error {
	var config map[string]interface{}
	if err := json.Unmarshal(configBytes, &config); err == nil {
		if key, ok := config["api_key"].(string); ok {
			p.apiKey = key
		}
		if url, ok := config["base_url"].(string); ok && url != "" {
			p.baseURL = url
		}
		if ver, ok := config["api_version"].(string); ok && ver != "" {
			p.apiVersion = ver
		}
	}
	return nil
}

func (p *Provider) Start(ctx context.Context) error {
	return nil
}

func (p *Provider) Stop(ctx context.Context) error {
	return nil
}

func (p *Provider) Health(ctx context.Context) v1.HealthStatus {
	return v1.HealthStatus{Healthy: true}
}

func (p *Provider) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	return time.Since(start), nil
}

func (p *Provider) ListModels(ctx context.Context) ([]v1.ModelDescriptor, error) {
	return []v1.ModelDescriptor{
		{ID: "gpt-4o", Name: "Azure GPT-4o Deployment"},
		{ID: "gpt-4-turbo", Name: "Azure GPT-4 Turbo Deployment"},
	}, nil
}

func (p *Provider) StreamChat(ctx context.Context, req *v1.ChatRequest) (<-chan v1.StreamChunk, error) {
	deployment := req.Model
	if deployment == "" {
		deployment = "gpt-4o"
	}

	endpoint := strings.TrimRight(p.baseURL, "/")
	// 若用户输入的 endpoint 已经包含了完整的 deployments 路径，则不再拼接
	if !strings.Contains(endpoint, "/openai/deployments/") {
		ver := p.apiVersion
		if ver == "" {
			ver = "2024-02-15-preview"
		}
		endpoint = fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s", endpoint, deployment, ver)
	}

	payload := map[string]interface{}{
		"messages": json.RawMessage(req.Messages),
		"stream":   true,
	}
	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("api-key", p.apiKey)
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Azure OpenAI HTTP %d: %s", resp.StatusCode, string(b))
	}

	ch := make(chan v1.StreamChunk, 64)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			var streamResp struct {
				Choices []struct {
					Delta struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(data), &streamResp); err == nil {
				for _, c := range streamResp.Choices {
					if c.Delta.ReasoningContent != "" {
						ch <- v1.StreamChunk{DeltaContent: c.Delta.ReasoningContent}
					}
					if c.Delta.Content != "" {
						ch <- v1.StreamChunk{DeltaContent: c.Delta.Content}
					}
				}
			}
		}
	}()

	return ch, nil
}
