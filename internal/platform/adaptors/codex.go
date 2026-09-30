package adaptors

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tiancode/internal/core/llm"
	"tiancode/internal/platform/codexauth"
	"tiancode/internal/platform/netproxy"
)

// Codex 是 OpenAI Codex（ChatGPT 订阅账号）协议适配器。
//
// 与 OpenAI 兼容协议的本质差异（照着协议事实实现，不是风格选择）：
//   - 上游端点 https://chatgpt.com/backend-api/codex/responses（Responses API，**不是** /chat/completions）；
//   - 请求体：system 提升为 instructions、input 为 item 数组、store 恒 false、stream 恒 true、
//     max_output_tokens/temperature 等字段上游不接受（不写）；
//   - 身份伪装三件套 originator/User-Agent/version 必须配套（错配上游直接 404），
//     另带 chatgpt-account-id（从 OAuth 凭证解析）；
//   - 凭证为 codexauth 单行 JSON（OAuth）；非 JSON 视作普通 Key（经代理的场景）。
//
// 流循环骨架与 openaiprovider 同一纪律（scanner 独立 goroutine + 取消/看门狗/数据三路
// select + 恰好一个终态 + 发送逃生），事件集换成本协议。
type Codex struct {
	// HTTPClient 可注入（测试用 httptest）；nil 用默认客户端。
	HTTPClient *http.Client
	// IdleTimeout 空闲看门狗阈值；<=0 取引擎默认 60s。
	IdleTimeout time.Duration
}

var _ Adaptor = (*Codex)(nil)

// 身份伪装常量：originator 与 UA 首段必须配套（升 version 时两处一起改）。
const (
	codexOriginator = "codex-tui"
	codexVersion    = "0.146.0"
	codexUserAgent  = "codex-tui/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color"
)

func (Codex) Type() string { return "codex" }

func (Codex) DefaultBaseURL() string { return "https://chatgpt.com/backend-api/codex" }

func (Codex) GetRequestURL(rc RouteContext) string {
	return WithAuthQuery(strings.TrimSuffix(rc.BaseURL, "/")+"/responses", rc)
}

// SetupHeaders 设置内容头、身份伪装头与鉴权。
// OAuth 凭证（JSON）：Bearer access_token + chatgpt-account-id；
// 其他凭证：走通用鉴权（缺省 Bearer，适配"经代理的 API Key"场景）。
func (Codex) SetupHeaders(rc RouteContext, hdr http.Header) error {
	hdr.Set("Content-Type", "application/json")
	hdr.Set("Accept", "text/event-stream")
	hdr.Set("OpenAI-Beta", "responses=experimental")
	hdr.Set("originator", codexOriginator)
	hdr.Set("User-Agent", codexUserAgent)
	hdr.Set("version", codexVersion)
	sid := codexSessionID(rc)
	hdr.Set("session_id", sid)
	hdr.Set("conversation_id", sid)

	if cred, ok := codexauth.Parse(rc.Credential); ok {
		if rc.Credential == "" || cred.AccessToken == "" {
			return errors.New("codex 凭证缺少 access_token（JSON 可能被截断）")
		}
		hdr.Set("Authorization", "Bearer "+cred.AccessToken)
		if cred.ChatGPTAccountID != "" {
			hdr.Set("chatgpt-account-id", cred.ChatGPTAccountID)
		}
		return nil
	}
	return ApplyAuth(rc, hdr, AuthDefaultBearer)
}

// ConvertRequest 把统一 Chat 请求转为 Responses 请求体。
// userText 取用户消息的文字：优先 Parts 里的文字段（0.0.25 附件轮的正文在那里），
// 没有 Parts 时回落 Content。
func userText(m llm.Message) string {
	if len(m.Parts) == 0 {
		return m.Content
	}
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	if b.Len() == 0 {
		return m.Content // Parts 全是图片等非文字片段时仍有正文可发
	}
	return b.String()
}

func (a Codex) ConvertRequest(rc RouteContext, req llm.ChatRequest) ([]byte, error) {
	var sysParts []string
	input := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			// system 消息提升为 instructions（ChatGPT 内部端点不接受 role:system）
			if s := strings.TrimSpace(m.Content); s != "" {
				sysParts = append(sysParts, s)
			}
		case "user":
			// 文本（0.0.25）：先取 Parts 里的文字段——附件轮的正文、内联文件内容与图片
			// 说明都在那里；没有 Parts 再回落 Content。此前只读 Content，附件轮里它是
			// 空串，模型收到的是一条空 input_text（等于没发出去）。
			text := userText(m)
			// 图片：Responses 这条链路没有图片字段，**不发明** input_image——在文字里写明
			// "本协议未发送图像"与文件名（Parts 的图片片段带 Name），模型至少知道有图。
			var imgs []string
			for _, part := range m.Parts {
				if part.Type != "image_url" {
					continue
				}
				name := part.Name
				if name == "" {
					name = "未命名图片"
				}
				imgs = append(imgs, fmt.Sprintf("\n[本协议未发送图像：%s]", name))
			}
			text += strings.Join(imgs, "")
			if strings.TrimSpace(text) == "" {
				// 上游不接受空串 input_text：宁可写一句事实，也不让这条消息成空壳
				text = "[本轮没有可发送的文字内容]"
			}
			input = append(input, map[string]any{
				"type": "message", "role": "user",
				"content": []map[string]any{{"type": "input_text", "text": text}},
			})
		case "assistant":
			if strings.TrimSpace(m.Content) != "" {
				input = append(input, map[string]any{
					"type": "message", "role": "assistant",
					"content": []map[string]any{{"type": "output_text", "text": m.Content}},
				})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				input = append(input, map[string]any{
					"type": "function_call", "call_id": normalizeCallID(tc.ID),
					"name": tc.Name, "arguments": args,
				})
			}
		case "tool":
			out := m.Content
			if strings.TrimSpace(out) == "" {
				out = "(empty)"
			}
			input = append(input, map[string]any{
				"type": "function_call_output", "call_id": normalizeCallID(m.ToolCallID), "output": out,
			})
		}
	}

	body := map[string]any{
		"model":        rc.Model,
		"instructions": strings.Join(sysParts, "\n\n"), // 恒存在（可空串：上游要求该字段）
		"input":        input,
		"stream":       true, // 上游恒流式
		"store":        false,
		"include":      []string{"reasoning.encrypted_content"},
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := any(map[string]any{})
			if len(t.Parameters) > 0 {
				params = json.RawMessage(t.Parameters)
			}
			tools = append(tools, map[string]any{
				"type": "function", "name": t.Name, "description": t.Description,
				"parameters": params, "strict": false,
			})
		}
		body["tools"] = tools
	}
	return json.Marshal(body)
}

func (a Codex) DoRequest(ctx context.Context, rc RouteContext, hdr http.Header, body []byte) (*http.Response, error) {
	client, err := netproxy.Client(a.HTTPClient, rc.Proxy)
	if err != nil {
		return nil, err
	}
	return DoJSON(ctx, client, a.GetRequestURL(rc), hdr, body)
}

// ConvertResponse 校验 HTTP 状态并启动 Codex SSE 流解析。
func (a Codex) ConvertResponse(ctx context.Context, rc RouteContext, resp *http.Response) (<-chan llm.StreamChunk, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := ReadErrBody(resp)
		if err := resp.Body.Close(); err != nil {
			msg = strings.TrimSpace(msg + "（且响应体关闭失败：" + err.Error() + "）")
		}
		return nil, fmt.Errorf("上游 HTTP %d：%s", resp.StatusCode, msg)
	}
	out := make(chan llm.StreamChunk)
	go a.stream(ctx, resp.Body, out)
	return out, nil
}

// ---- 流解析（骨架同 openaiprovider：三路 select + 恰好一个终态 + 发送逃生）----

type codexStreamState struct {
	terminalSent bool
	finishing    bool
}

func (a Codex) stream(ctx context.Context, body io.ReadCloser, out chan llm.StreamChunk) {
	defer close(out)
	defer body.Close()

	idle := a.IdleTimeout
	if idle <= 0 {
		idle = IdleTimeout
	}

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

	st := &codexStreamState{}
	// decide 发出终态（恰好一个）并解除 scanner 阻塞
	decide := func(reason llm.EndReason, err error, usage *llm.Usage) {
		if st.terminalSent {
			return
		}
		st.terminalSent = true
		st.finishing = true
		sendChunk(ctx, out, llm.StreamChunk{Err: err, EndReason: reason, Usage: usage})
		body.Close() // 关闭失败无补救动作（终态已定）；裸调用是显式选择
	}
	for {
		select {
		case <-ctx.Done():
			decide(llm.EndCancelled, ctx.Err(), nil)
			return
		case <-watchdog.C:
			decide(llm.EndIdleTimeout, nil, nil)
			return
		case err := <-scanErrC:
			if !st.finishing {
				if err != nil {
					decide(llm.EndError, fmt.Errorf("上游连接错误：%w", err), nil)
				} else {
					decide(llm.EndError, errors.New("上游流提前关闭（无终态事件）"), nil)
				}
			}
			return
		case line := <-lineC:
			watchdog.Reset(idle)
			if st.finishing {
				continue
			}
			if !handleCodexLine(ctx, out, line, decide) {
				return // 消费方已离开
			}
			if st.finishing {
				return
			}
		}
	}
}

// sendChunk 发送块的"逃生"语义：消费方离开（ctx 取消）时放弃发送，返回 false。
func sendChunk(ctx context.Context, out chan llm.StreamChunk, chunk llm.StreamChunk) bool {
	select {
	case out <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}

// codexEvent 是 Responses SSE 事件（只声明用到的字段）。
type codexEvent struct {
	Type        string `json:"type"`
	Delta       string `json:"delta"`
	OutputIndex int    `json:"output_index"`
	Item        *struct {
		Type   string `json:"type"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
	} `json:"item"`
	Response *struct {
		Usage *codexUsage `json:"usage"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
	Usage *codexUsage `json:"usage"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type codexUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

func (e codexEvent) usage() *llm.Usage {
	u := e.Usage
	if u == nil && e.Response != nil {
		u = e.Response.Usage
	}
	if u == nil {
		return nil
	}
	return &llm.Usage{PromptTokens: u.InputTokens, CompletionTokens: u.OutputTokens, TotalTokens: u.TotalTokens}
}

func (e codexEvent) errorMessage() string {
	if e.Error != nil && e.Error.Message != "" {
		return e.Error.Message
	}
	if e.Response != nil && e.Response.Error != nil && e.Response.Error.Message != "" {
		return e.Response.Error.Message
	}
	return "未知错误"
}

// handleCodexLine 处理一行 SSE；返回 false 表示消费方已离开，应立即退出。
func handleCodexLine(ctx context.Context, out chan llm.StreamChunk, line string, decide func(llm.EndReason, error, *llm.Usage)) bool {
	if !strings.HasPrefix(line, "data:") {
		return true // event:/id:/空行等忽略
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "" {
		return true
	}
	if data == "[DONE]" {
		// 上游通常不发；容错处理（未终态则正常收束）
		decide(llm.EndDone, nil, nil)
		return true
	}
	var ev codexEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return true // 非 JSON 行忽略（心跳等）
	}
	switch ev.Type {
	case "response.output_text.delta":
		if ev.Delta != "" {
			return sendChunk(ctx, out, llm.StreamChunk{Delta: ev.Delta})
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if ev.Delta != "" {
			return sendChunk(ctx, out, llm.StreamChunk{Thinking: ev.Delta})
		}
	case "response.output_item.added":
		// 仅函数调用项产生工具分片（首块带 id + name）
		if ev.Item != nil && (ev.Item.Type == "function_call" || ev.Item.Type == "custom_tool_call") {
			return sendChunk(ctx, out, llm.StreamChunk{ToolCalls: []llm.ToolCallChunk{{
				Index: ev.OutputIndex, ID: ev.Item.CallID, Name: ev.Item.Name,
			}}})
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		if ev.Delta != "" {
			return sendChunk(ctx, out, llm.StreamChunk{ToolCalls: []llm.ToolCallChunk{{
				Index: ev.OutputIndex, ArgumentsDelta: ev.Delta,
			}}})
		}
	case "response.completed", "response.done":
		decide(llm.EndDone, nil, ev.usage())
	case "response.incomplete":
		decide(llm.EndError, errors.New("响应未完成（达到长度上限或被内容过滤）"), ev.usage())
	case "response.failed":
		decide(llm.EndError, fmt.Errorf("上游失败：%s", ev.errorMessage()), nil)
	case "error":
		decide(llm.EndError, fmt.Errorf("上游错误：%s", ev.errorMessage()), nil)
	default:
		// response.created / in_progress / output_item.done / *_part.* 等不产出
	}
	return true
}

// normalizeCallID 规范化函数调用 ID：Codex 要求 fc_/ctc_/tsc_ 前缀（call_* 转 fc_*）；
// 超长用确定性哈希截断（保证 function_call 与 function_call_output 两侧一致）。
func normalizeCallID(id string) string {
	s := strings.TrimSpace(id)
	if s == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(s, "fc_"), strings.HasPrefix(s, "ctc_"), strings.HasPrefix(s, "tsc_"):
	case strings.HasPrefix(s, "call_"):
		s = "fc_" + strings.TrimPrefix(s, "call_")
	default:
		s = "fc_" + s
	}
	if len(s) > 64 {
		sum := sha256.Sum256([]byte("tiancode:codex-call-id:v1:" + s))
		s = "fc_" + hex.EncodeToString(sum[:])[:61]
	}
	return s
}

// codexSessionID 生成确定性会话 ID（UUIDv4 形态）：同渠道同凭证稳定——
// 会话频繁漂移会影响上游侧缓存与配额归属。
func codexSessionID(rc RouteContext) string {
	sum := sha256.Sum256([]byte("tiancode:codex-session:v1:" + rc.ChannelID + "|" + rc.Credential))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
