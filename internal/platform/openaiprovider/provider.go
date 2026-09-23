// Package openaiprovider 实现 core/llm.ProviderPort 的 OpenAI 兼容流式适配器。
//
// 做什么：把 OpenAI 兼容的 /chat/completions SSE 流转换为 core/llm 的
// StreamChunk 通道，并强制执行流式纪律（EndReason 终态契约，见 ADR-0003）。
// 被谁依赖：internal/core/llm 的 ChatRuntime（经端口注入，agent 不感知本包）。
// 依赖谁：core/llm 端口 + stdlib。
//
// 流式纪律（契约 C-LLM-1~7，参照 new-api relay/helper/stream_scanner.go）：
//   - 空闲看门狗：每收到一行重置；超时以 EndIdleTimeout 收束，绝不永久阻塞；
//   - 发送逃生：数据块发送经 select ctx.Done 逃生，消费方离开即退出（无泄漏）；
//   - 恰好一个终态：EndReason != EndNone 的块整流恰好一个，之后 close(channel)；
//   - 连接中断（scanner 错误）与流内错误报文都以 EndError 收束，不静默。
package openaiprovider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tiancode/internal/core/llm"
)

// 编译期保证实现端口契约。
var _ llm.ProviderPort = (*Provider)(nil)

// terminalGrace 是终态块的投递等待上限。
// 为什么 500ms：消费方取消 ctx 后通常仍在排空通道以获取终态，给一次阻塞投递机会；
// 消费方确已离开时最多延迟 500ms 关闭，杜绝 goroutine 泄漏。
const terminalGrace = 500 * time.Millisecond

// Options 是 provider 构造参数。
type Options struct {
	BaseURL     string        // OpenAI 兼容网关根地址（如 https://api.deepseek.com/v1）
	APIKey      string        // Bearer 令牌；仅从环境变量/用户配置注入，禁止硬编码
	HTTPClient  *http.Client  // 可注入（测试用 httptest）；nil 则用默认客户端
	IdleTimeout time.Duration // 空闲看门狗阈值；<=0 时取默认 60s
}

// Provider 是 OpenAI 兼容流式适配器。
type Provider struct {
	opts Options
}

// New 构造 provider 并补齐默认值。
func New(opts Options) *Provider {
	if opts.IdleTimeout <= 0 {
		// 为什么 60s：主流网关在推理中每隔数秒必有心跳/增量，60s 无数据视为挂起。
		opts.IdleTimeout = 60 * time.Second
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	return &Provider{opts: opts}
}

// StreamChat 发起流式对话补全（实现 core/llm.ProviderPort）。
// HTTP 非 2xx 以 error 返回（流未开始）——该形态是 ChatRuntime 流前重试（C-RT-1）的前提。
func (p *Provider) StreamChat(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	payload, err := json.Marshal(struct {
		Model    string        `json:"model"`
		Messages []llm.Message `json:"messages"`
		Stream   bool          `json:"stream"`
	}{req.Model, req.Messages, true})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(p.opts.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.opts.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.opts.APIKey)
	}

	resp, err := p.opts.HTTPClient.Do(httpReq)
	if err != nil {
		// 连接失败发生在流开始前：以 error 返回，交由 ChatRuntime 决定是否重试
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		return nil, fmt.Errorf("upstream returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	out := make(chan llm.StreamChunk)
	go p.stream(ctx, resp.Body, out)
	return out, nil
}

// streamState 记录流处理过程中的终态决策。
type streamState struct {
	terminalSent bool // 是否已发出终态块（恰好一个，C-LLM-7）
	finishing    bool // 已决定终态：继续读行仅为排空 scanner
	finishSeen   bool // 已收到非空 finish_reason（EOF 无 [DONE] 时据此判 EndDone）
}

// stream 消费上游 SSE 并向 out 输出块；返回前保证 close(out) 恰好一次。
func (p *Provider) stream(ctx context.Context, body io.ReadCloser, out chan llm.StreamChunk) {
	defer close(out)
	defer body.Close()

	// scanner 与主循环分离：bufio.Scanner.Scan() 无法被 select 中断，
	// 独立 goroutine 逐行喂 lineC，主循环才能同时响应 取消/看门狗/数据。
	lineC := make(chan string)
	scanErrC := make(chan error, 1) // buffered：主循环已退出时 scanner 结束也不阻塞
	go func() {
		scanner := bufio.NewScanner(body)
		// 为什么 10MB：长思考（reasoning）单分片可能极大，默认 64KB 会截断长行
		scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
		for scanner.Scan() {
			select {
			case lineC <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		scanErrC <- scanner.Err()
	}()

	watchdog := time.NewTimer(p.opts.IdleTimeout)
	defer watchdog.Stop()

	var st streamState
	// decide 发出终态后立即关闭 body：解除 scanner 在挂起连接上的阻塞
	//（scanErrC 缓冲 1，scanner goroutine 随后自行退出，无泄漏）。
	// 为什么不"排空到 scanner 结束"：上游挂起时 Scan() 可能永远不返回，等待即卡死。
	decide := func(reason llm.EndReason, err error) {
		p.terminal(out, reason, err, &st)
		st.finishing = true
		body.Close()
	}
	for {
		select {
		case <-ctx.Done():
			decide(llm.EndCancelled, ctx.Err())
			return
		case <-watchdog.C:
			decide(llm.EndIdleTimeout, nil)
			return
		case err := <-scanErrC:
			if !st.finishing {
				switch {
				case err != nil:
					decide(llm.EndError, fmt.Errorf("upstream connection error: %w", err))
				case st.finishSeen:
					// 服务端发完 finish_reason 直接关流（未发 [DONE]）也算正常完成
					decide(llm.EndDone, nil)
				default:
					decide(llm.EndError, errors.New("upstream closed stream without finish_reason or [DONE]"))
				}
			}
			return // scanner 已结束；channel 由 defer 关闭
		case line := <-lineC:
			watchdog.Reset(p.opts.IdleTimeout)
			if st.finishing {
				continue // 终态已定：忽略剩余行（如 [DONE]）
			}
			if p.handleLine(ctx, out, line, &st) {
				return // 消费方已离开（发送逃生），channel 由 defer 关闭
			}
			if st.finishing {
				// handleLine 内决定了终态（finish_reason / [DONE] / 流内错误）
				body.Close()
				return
			}
		}
	}
}

// handleLine 处理单行 SSE。返回 true 表示消费方已离开，应立即退出。
func (p *Provider) handleLine(ctx context.Context, out chan llm.StreamChunk, line string, st *streamState) bool {
	if !strings.HasPrefix(line, "data: ") {
		return false
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
	if data == "" {
		return false
	}
	if data == "[DONE]" {
		p.terminal(out, llm.EndDone, nil, st)
		st.finishing = true
		return false
	}

	// 上游错误报文 fail-closed（契约 C-LLM-4）：绝不静默吞掉
	var errPayload struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &errPayload); err == nil && errPayload.Error != nil && errPayload.Error.Message != "" {
		p.terminal(out, llm.EndError, fmt.Errorf("upstream API error: %s", errPayload.Error.Message), st)
		st.finishing = true
		return false
	}

	var sse struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"` // DeepSeek R1 思考流
				Reasoning        string `json:"reasoning"`         // 网关思考流别名字段
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Name     string `json:"name"`
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &sse); err != nil {
		// 无法解析的行视为上游噪声：丢弃继续（终态语义不受影响）
		return false
	}

	if len(sse.Choices) == 0 {
		if sse.Usage != nil {
			return p.send(ctx, out, llm.StreamChunk{Usage: &llm.Usage{
				PromptTokens:     sse.Usage.PromptTokens,
				CompletionTokens: sse.Usage.CompletionTokens,
				TotalTokens:      sse.Usage.TotalTokens,
			}})
		}
		return false
	}
	choice := sse.Choices[0]

	if choice.FinishReason != nil && *choice.FinishReason != "" {
		st.finishSeen = true
		p.terminal(out, llm.EndDone, nil, st)
		st.finishing = true
		return false
	}

	chunk := llm.StreamChunk{
		Delta:    choice.Delta.Content,
		Thinking: choice.Delta.ReasoningContent,
	}
	if chunk.Thinking == "" {
		chunk.Thinking = choice.Delta.Reasoning
	}
	if len(choice.Delta.ToolCalls) > 0 {
		chunk.ToolCalls = make([]llm.ToolCallChunk, 0, len(choice.Delta.ToolCalls))
		for _, tc := range choice.Delta.ToolCalls {
			chunk.ToolCalls = append(chunk.ToolCalls, llm.ToolCallChunk{
				Index:          tc.Index,
				ID:             tc.ID,
				Name:           tc.Name,
				ArgumentsDelta: tc.Function.Arguments,
			})
		}
	}
	if sse.Usage != nil {
		chunk.Usage = &llm.Usage{
			PromptTokens:     sse.Usage.PromptTokens,
			CompletionTokens: sse.Usage.CompletionTokens,
			TotalTokens:      sse.Usage.TotalTokens,
		}
	}
	// 全空块跳过：避免无意义流量干扰消费方的终态统计
	if chunk.Delta == "" && chunk.Thinking == "" && chunk.ToolCalls == nil && chunk.Usage == nil {
		return false
	}
	return p.send(ctx, out, chunk)
}

// send 发送数据块；消费方离开（ctx 取消期间发送受阻）时返回 true。
// 这是 C-LLM-6 的逃生通道：绝不永久阻塞在 channel 发送上。
func (p *Provider) send(ctx context.Context, out chan llm.StreamChunk, c llm.StreamChunk) bool {
	select {
	case out <- c:
		return false
	case <-ctx.Done():
		return true
	}
}

// terminal 投递恰好一个终态块（C-LLM-7）。
// 投递最多等待 terminalGrace：给仍在排空的消费方机会；已离开时由 close(out) 兜底。
func (p *Provider) terminal(out chan llm.StreamChunk, reason llm.EndReason, err error, st *streamState) {
	if st.terminalSent {
		return
	}
	st.terminalSent = true
	c := llm.StreamChunk{EndReason: reason, Err: err}
	select {
	case out <- c:
	case <-time.After(terminalGrace):
	}
}
