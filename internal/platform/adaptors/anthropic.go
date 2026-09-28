package adaptors

import (
	"bufio"
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

// Anthropic 是 Anthropic Messages 协议适配器（/v1/messages + SSE）。
// 入站统一请求（OpenAI Chat 形态）→ Anthropic 报文；响应 SSE → StreamChunk。
// 鉴权：x-api-key + anthropic-version（区别于 Bearer，验证"凭证只在适配器内解释"）。
type Anthropic struct {
	HTTPClient  *http.Client
	IdleTimeout time.Duration
	// Version 是 anthropic-version 头；空取默认（可被渠道 header_override 覆盖）。
	Version string
}

var _ Adaptor = (*Anthropic)(nil)

// DefaultMaxTokens：Anthropic 必填 max_tokens；统一请求没有该字段，给安全默认值
// （可被渠道 param_override 覆盖）。
const DefaultMaxTokens = 8192

func (Anthropic) Type() string           { return "anthropic" }
func (Anthropic) DefaultBaseURL() string { return "https://api.anthropic.com/v1" }

func (Anthropic) GetRequestURL(rc RouteContext) string {
	return WithAuthQuery(strings.TrimSuffix(rc.BaseURL, "/")+"/messages", rc)
}

// SetupHeaders 设置内容头与鉴权：缺省 x-api-key（Anthropic 原生形态），
// 渠道可覆盖为 Bearer 或任意头（很多中转把 Claude 放在 Authorization: Bearer 之后）。
func (a Anthropic) SetupHeaders(rc RouteContext, hdr http.Header) error {
	hdr.Set("Content-Type", "application/json")
	hdr.Set("Accept", "text/event-stream")
	if err := ApplyAuth(rc, hdr, AuthDefaultAnthropicKey); err != nil {
		return err
	}
	version := a.Version
	if version == "" {
		version = "2023-06-01"
	}
	hdr.Set("anthropic-version", version)
	return nil
}

// ---- wire 类型：Anthropic 私有格式 ----

type antBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result
	Content   string          `json:"content,omitempty"`     // tool_result
}

type antMessage struct {
	Role    string     `json:"role"`
	Content []antBlock `json:"content"`
}

type antTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type antRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    string       `json:"system,omitempty"`
	Messages  []antMessage `json:"messages"`
	Tools     []antTool    `json:"tools,omitempty"`
	Stream    bool         `json:"stream"`
}

// ConvertRequest 把统一请求转为 Anthropic Messages 报文。
// 映射要点：system 消息提升为顶层 system；role=tool 连续段合并为一条 user 消息的
// tool_result 块；assistant 的 ToolCalls → tool_use 块；max_tokens 必填给默认值。
func (a Anthropic) ConvertRequest(rc RouteContext, req llm.ChatRequest) ([]byte, error) {
	out := antRequest{
		Model:     rc.Model,
		MaxTokens: DefaultMaxTokens,
		Stream:    true,
	}
	var system strings.Builder
	flushToolResults := func(role string, blocks []antBlock) {
		if len(blocks) > 0 {
			out.Messages = append(out.Messages, antMessage{Role: role, Content: blocks})
		}
	}
	pendingRole := ""
	pendingBlocks := []antBlock{}
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			if system.Len() > 0 {
				system.WriteString("\n")
			}
			system.WriteString(m.Content)
		case "user", "assistant":
			// 角色切换或遇到 tool 段时，先落已累积的消息（tool_result 段必须整体成一条 user 消息）
			if pendingRole != "" && (pendingRole != m.Role) {
				flushToolResults(pendingRole, pendingBlocks)
				pendingRole, pendingBlocks = "", nil
			}
			blocks := []antBlock{}
			if m.Content != "" {
				blocks = append(blocks, antBlock{Type: "text", Text: m.Content})
			}
			if m.Role == "assistant" {
				for _, tc := range m.ToolCalls {
					input := json.RawMessage(tc.Arguments)
					if !json.Valid(input) || len(input) == 0 {
						input = json.RawMessage("{}")
					}
					blocks = append(blocks, antBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
				}
			}
			if pendingRole == "" {
				pendingRole = m.Role
			}
			pendingBlocks = append(pendingBlocks, blocks...)
		case "tool":
			// tool_result 必须挂在 user 消息上；若上一段是 assistant 先落盘
			if pendingRole == "assistant" {
				flushToolResults(pendingRole, pendingBlocks)
				pendingRole, pendingBlocks = "", nil
			}
			if pendingRole == "" {
				pendingRole = "user"
			}
			pendingBlocks = append(pendingBlocks, antBlock{
				Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content,
			})
		}
	}
	flushToolResults(pendingRole, pendingBlocks)
	out.System = strings.TrimSpace(system.String())
	for _, d := range req.Tools {
		schema := d.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out.Tools = append(out.Tools, antTool{Name: d.Name, Description: d.Description, InputSchema: schema})
	}
	if mt, ok := rc.ParamOverride["max_tokens"].(float64); ok && mt > 0 {
		out.MaxTokens = int(mt)
	}
	return json.Marshal(out)
}

func (a Anthropic) DoRequest(ctx context.Context, rc RouteContext, hdr http.Header, body []byte) (*http.Response, error) {
	return DoJSON(ctx, a.HTTPClient, a.GetRequestURL(rc), hdr, body)
}

// ConvertResponse 解析 Anthropic SSE（message_start / content_block_* / message_delta /
// message_stop / error），转回流式块。流式纪律与 OpenAI 引擎一致：
// 空闲看门狗 / 发送逃生 / 恰好一个终态。
func (a Anthropic) ConvertResponse(ctx context.Context, rc RouteContext, resp *http.Response) (<-chan llm.StreamChunk, error) {
	out := make(chan llm.StreamChunk)
	idle := a.IdleTimeout
	if idle <= 0 {
		idle = IdleTimeout
	}
	go a.stream(ctx, resp.Body, out, idle)
	return out, nil
}

// antStreamState 记录流处理状态（与 openaiprovider.streamState 同语义）。
type antStreamState struct {
	terminalSent bool
	finishing    bool
	stopSeen     bool
	promptTokens int64
	// 当前内容块：tool_use 聚合 input_json_delta
	blockType string
	toolID    string
	toolName  string
	toolArgs  strings.Builder
	toolIndex int
}

// stream 消费 SSE 行并向 out 输出块；返回前保证 close(out) 恰好一次、body 恰好关闭一次。
func (a Anthropic) stream(ctx context.Context, body io.ReadCloser, out chan llm.StreamChunk, idle time.Duration) {
	defer close(out)
	defer body.Close()

	lineC := make(chan string)
	scanErrC := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(body)
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

	watchdog := time.NewTimer(idle)
	defer watchdog.Stop()

	var st antStreamState
	decide := func(reason llm.EndReason, err error) {
		a.terminal(out, reason, err, &st)
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
				case st.stopSeen:
					decide(llm.EndDone, nil)
				default:
					decide(llm.EndError, errors.New("upstream closed stream without message_stop"))
				}
			}
			return
		case line := <-lineC:
			watchdog.Reset(idle)
			if st.finishing {
				continue
			}
			if a.handleEvent(ctx, out, line, &st) {
				return
			}
			if st.finishing {
				body.Close()
				return
			}
		}
	}
}

// handleEvent 处理单个 SSE 事件（event: + data: 成对到达，只关心 data 载荷）。
// 返回 true 表示消费方已离开。
func (a Anthropic) handleEvent(ctx context.Context, out chan llm.StreamChunk, line string, st *antStreamState) bool {
	if !strings.HasPrefix(line, "data: ") {
		return false
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
	if data == "" || data == "[DONE]" {
		return false
	}

	// 流内错误报文 fail-closed
	var errPayload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &errPayload); err == nil && errPayload.Error.Message != "" {
		a.terminal(out, llm.EndError, fmt.Errorf("upstream API error: %s", errPayload.Error.Message), st)
		st.finishing = true
		return false
	}

	var ev struct {
		Type string `json:"type"`
		// message_start
		Message struct {
			Usage struct {
				InputTokens int64 `json:"input_tokens"`
			} `json:"usage"`
		} `json:"message"`
		// content_block_start / content_block_delta / content_block_stop
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
		Index int `json:"index"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
		// message_delta
		Usage struct {
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return false // 无法解析的行视为噪声丢弃
	}

	switch ev.Type {
	case "ping":
		return false
	case "message_start":
		st.promptTokens = ev.Message.Usage.InputTokens
		return false
	case "content_block_start":
		st.blockType = ev.ContentBlock.Type
		st.toolID = ev.ContentBlock.ID
		st.toolName = ev.ContentBlock.Name
		st.toolArgs.Reset()
		return false
	case "content_block_delta":
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text == "" {
				return false
			}
			return a.send(ctx, out, llm.StreamChunk{Delta: ev.Delta.Text})
		case "thinking_delta":
			if ev.Delta.Thinking == "" {
				return false
			}
			return a.send(ctx, out, llm.StreamChunk{Thinking: ev.Delta.Thinking})
		case "input_json_delta":
			st.toolArgs.WriteString(ev.Delta.PartialJSON)
		}
		return false
	case "content_block_stop":
		if st.blockType == "tool_use" {
			args := st.toolArgs.String()
			if args == "" {
				args = "{}"
			}
			left := a.send(ctx, out, llm.StreamChunk{ToolCalls: []llm.ToolCallChunk{{
				Index:          st.toolIndex,
				ID:             st.toolID,
				Name:           st.toolName,
				ArgumentsDelta: args,
			}}})
			st.toolIndex++
			st.blockType = ""
			return left
		}
		st.blockType = ""
		return false
	case "message_delta":
		if ev.Usage.OutputTokens > 0 {
			if a.send(ctx, out, llm.StreamChunk{Usage: &llm.Usage{
				PromptTokens:     st.promptTokens,
				CompletionTokens: ev.Usage.OutputTokens,
				TotalTokens:      st.promptTokens + ev.Usage.OutputTokens,
			}}) {
				return true
			}
		}
		// stop_reason 到达即完成（tool_use 的调用块已在 content_block_stop 转发）
		st.stopSeen = true
		a.terminal(out, llm.EndDone, nil, st)
		st.finishing = true
		return false
	case "message_stop":
		if !st.stopSeen {
			st.stopSeen = true
			a.terminal(out, llm.EndDone, nil, st)
			st.finishing = true
		}
		return false
	case "error":
		// 已在上方 error 载荷分支处理；带 type 字段的错误也兜住
		a.terminal(out, llm.EndError, fmt.Errorf("upstream API error: %s", data), st)
		st.finishing = true
		return false
	}
	return false
}

// send 发送数据块；消费方离开时返回 true（发送逃生）。
func (a Anthropic) send(ctx context.Context, out chan llm.StreamChunk, c llm.StreamChunk) bool {
	select {
	case out <- c:
		return false
	case <-ctx.Done():
		return true
	}
}

// terminal 投递恰好一个终态块（最多等待 terminalGrace）。
func (a Anthropic) terminal(out chan llm.StreamChunk, reason llm.EndReason, err error, st *antStreamState) {
	if st.terminalSent {
		return
	}
	st.terminalSent = true
	c := llm.StreamChunk{EndReason: reason, Err: err}
	select {
	case out <- c:
	case <-time.After(500 * time.Millisecond):
	}
}
