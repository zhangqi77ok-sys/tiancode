package anthropic

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
	apiKey  string
	baseURL string
}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) ID() string {
	return "provider.anthropic"
}

func (p *Provider) Name() string {
	return "anthropic"
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
	key, _ := config["api_key"].(string)
	p.apiKey = key
	url, _ := config["base_url"].(string)
	if url != "" {
		p.baseURL = url
	} else {
		p.baseURL = "https://api.anthropic.com"
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

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

func (p *Provider) StreamChat(ctx context.Context, req *v1.ChatRequest) (<-chan v1.StreamChunk, error) {
	var rawMsgs []map[string]interface{}
	if err := json.Unmarshal(req.Messages, &rawMsgs); err != nil {
		return nil, fmt.Errorf("failed to parse messages: %w", err)
	}

	systemStr := ""
	var messages []anthropicMessage

	for _, m := range rawMsgs {
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		if role == "system" {
			systemStr += content + "\n"
		} else {
			messages = append(messages, anthropicMessage{
				Role:    role,
				Content: content,
			})
		}
	}

	payload := map[string]interface{}{
		"model":      req.Model,
		"max_tokens": 8192,
		"messages":   messages,
		"stream":     true,
	}
	if systemStr != "" {
		payload["system"] = strings.TrimSpace(systemStr)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(p.baseURL, "/")
	if !strings.HasSuffix(endpoint, "/messages") {
		endpoint = endpoint + "/v1/messages"
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
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
		return nil, fmt.Errorf("anthropic error %d: %s", resp.StatusCode, string(b))
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
			var evt map[string]interface{}
			if err := json.Unmarshal([]byte(data), &evt); err == nil {
				typ, _ := evt["type"].(string)
				if typ == "content_block_delta" {
					if delta, ok := evt["delta"].(map[string]interface{}); ok {
						if text, ok := delta["text"].(string); ok {
							ch <- v1.StreamChunk{DeltaContent: text}
						}
					}
				}
			}
		}
	}()
	return ch, nil
}

func (p *Provider) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	return time.Since(start), nil
}

func (p *Provider) ListModels(ctx context.Context) ([]v1.ModelDescriptor, error) {
	return []v1.ModelDescriptor{
		{ID: "claude-3-5-sonnet-20240620", Name: "Claude 3.5 Sonnet"},
		{ID: "claude-3-opus-20240229", Name: "Claude 3 Opus"},
	}, nil
}