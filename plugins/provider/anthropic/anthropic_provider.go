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
	"tiancode/pkg/protocol"
)

type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewProvider() *Provider {
	return &Provider{
		httpClient: &http.Client{Timeout: 0},
	}
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
	_ = json.Unmarshal(configBytes, &config)
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

func (p *Provider) StreamChat(ctx context.Context, req *v1.ChatRequest) (<-chan v1.StreamChunk, error) {
	if p.apiKey == "" && !strings.Contains(p.baseURL, "localhost") && !strings.Contains(p.baseURL, "127.0.0.1") {
		return nil, fmt.Errorf("未配置 Anthropic API Key，拒绝发起上游请求")
	}

	var rawMsgs []map[string]any
	if err := json.Unmarshal(req.Messages, &rawMsgs); err != nil {
		return nil, fmt.Errorf("failed to parse messages: %w", err)
	}

	systemStr := ""
	var messages []protocol.ClaudeMessage

	for _, m := range rawMsgs {
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		if role == "system" {
			if systemStr != "" {
				systemStr += "\n\n"
			}
			systemStr += content
		} else {
			claudeRole := role
			if claudeRole != "user" && claudeRole != "assistant" {
				claudeRole = "user"
			}
			messages = append(messages, protocol.ClaudeMessage{
				Role: claudeRole,
				Content: []protocol.ClaudeBlock{
					{Type: "text", Text: content},
				},
			})
		}
	}

	// 注入 Breakpoint 2: 在倒数第 2 轮历史 User 消息末尾挂载 ephemeral 缓存断点
	protocol.InjectClaudeMessageCacheBreakpoint(messages)

	payload := map[string]any{
		"model":      req.Model,
		"max_tokens": 8192,
		"messages":   messages,
		"stream":     true,
	}

	if strings.TrimSpace(systemStr) != "" {
		payload["system"] = protocol.NormalizeNewlines(strings.TrimSpace(systemStr))
	}

	// 注入工具定义并挂载 Breakpoint 1 (在 tools 列表最后一个工具上挂载 ephemeral 缓存断点)
	if len(req.Tools) > 0 {
		claudeTools := protocol.ConvertToolsToClaude(req.Tools, true)
		payload["tools"] = claudeTools
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal anthropic request: %w", err)
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
	httpReq.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("accept", "text/event-stream")

	resp, err := p.httpClient.Do(httpReq)
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
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 10*1024*1024)

		var currentPromptTokens int64
		var currentCacheReadTokens int64
		var currentCacheCreationTokens int64

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}

			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var evt map[string]any
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				continue
			}

			typ, _ := evt["type"].(string)
			switch typ {
			case "message_start":
				if msgObj, ok := evt["message"].(map[string]any); ok {
					if usageObj, ok := msgObj["usage"].(map[string]any); ok {
						if inTok, ok := usageObj["input_tokens"].(float64); ok {
							currentPromptTokens = int64(inTok)
						}
						if crTok, ok := usageObj["cache_read_input_tokens"].(float64); ok {
							currentCacheReadTokens = int64(crTok)
						}
						if ccTok, ok := usageObj["cache_creation_input_tokens"].(float64); ok {
							currentCacheCreationTokens = int64(ccTok)
						}
						ch <- v1.StreamChunk{
							Usage: &v1.TokenUsage{
								PromptTokens:        currentPromptTokens,
								CacheReadTokens:     currentCacheReadTokens,
								CacheCreationTokens: currentCacheCreationTokens,
							},
						}
					}
				}

			case "content_block_start":
				if cb, ok := evt["content_block"].(map[string]any); ok {
					cbType, _ := cb["type"].(string)
					idx := 0
					if idxF, ok := evt["index"].(float64); ok {
						idx = int(idxF)
					}
					if cbType == "tool_use" {
						id, _ := cb["id"].(string)
						name, _ := cb["name"].(string)
						ch <- v1.StreamChunk{
							ToolCalls: []v1.ToolCallChunk{
								{
									Index: idx,
									ID:    id,
									Name:  name,
								},
							},
						}
					}
				}

			case "content_block_delta":
				idx := 0
				if idxF, ok := evt["index"].(float64); ok {
					idx = int(idxF)
				}
				if delta, ok := evt["delta"].(map[string]any); ok {
					deltaType, _ := delta["type"].(string)
					switch deltaType {
					case "text_delta":
						if text, ok := delta["text"].(string); ok {
							ch <- v1.StreamChunk{DeltaContent: text}
						}
					case "thinking_delta":
						if thinking, ok := delta["thinking"].(string); ok {
							ch <- v1.StreamChunk{Thinking: thinking}
						}
					case "input_json_delta":
						if partialJSON, ok := delta["partial_json"].(string); ok {
							ch <- v1.StreamChunk{
								ToolCalls: []v1.ToolCallChunk{
									{
										Index:          idx,
										ArgumentsDelta: partialJSON,
									},
								},
							}
						}
					}
				}

			case "message_delta":
				finishReason := ""
				if delta, ok := evt["delta"].(map[string]any); ok {
					if sr, ok := delta["stop_reason"].(string); ok {
						finishReason = sr
					}
				}
				var outTokens int64
				if usageObj, ok := evt["usage"].(map[string]any); ok {
					if outTok, ok := usageObj["output_tokens"].(float64); ok {
						outTokens = int64(outTok)
					}
				}
				ch <- v1.StreamChunk{
					FinishReason: finishReason,
					Usage: &v1.TokenUsage{
						PromptTokens:        currentPromptTokens,
						CompletionTokens:    outTokens,
						TotalTokens:         currentPromptTokens + outTokens,
						CacheReadTokens:     currentCacheReadTokens,
						CacheCreationTokens: currentCacheCreationTokens,
					},
				}

			case "error":
				if errObj, ok := evt["error"].(map[string]any); ok {
					msg, _ := errObj["message"].(string)
					ch <- v1.StreamChunk{Error: fmt.Errorf("anthropic stream error: %s", msg)}
					return
				}
			}
		}

		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			ch <- v1.StreamChunk{Error: fmt.Errorf("anthropic scanner error: %w", err)}
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
		{ID: "claude-3-7-sonnet-20250219", Name: "Claude 3.7 Sonnet (Thinking)", SupportThinking: true, SupportCaching: true},
		{ID: "claude-3-5-sonnet-20241022", Name: "Claude 3.5 Sonnet v2", SupportCaching: true},
		{ID: "claude-3-5-haiku-20241022", Name: "Claude 3.5 Haiku", SupportCaching: true},
		{ID: "claude-3-opus-20240229", Name: "Claude 3 Opus", SupportCaching: true},
	}, nil
}