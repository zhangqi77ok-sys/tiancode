package mcpclient

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
	"sync"
)

// HTTP 传输（MCP Streamable HTTP，2026-10-19）。此前远程服务器只发一次裸 tools/call：
// 不做 initialize、不带 Mcp-Session-Id——按规范要求先握手的服务器一律拒绝；也不能
// tools/list，模型只能盲猜工具名与参数。现在每次使用建立一个会话：
//
//	initialize → 记下 Mcp-Session-Id 与协商版本 → notifications/initialized →
//	tools/list / tools/call（带会话头）→ DELETE 结束会话（尽力而为）
//
// 响应可能是 application/json，也可能是 text/event-stream（SSE，期间可能夹带服务器
// 通知）：按 JSON-RPC id 找到本次请求的响应。

// httpResponseLimit 是单次响应体上限（与旧 CallHTTP 同量级）。
const httpResponseLimit = 4 << 20

// HTTPSession 是与一台远程 MCP 服务器的一次会话。
type HTTPSession struct {
	endpoint  string
	headers   http.Header
	client    *http.Client
	mu        sync.Mutex
	seq       int
	sessionID string
	version   string
}

// DialHTTP 建立会话并完成握手。headers 是"键=值"逐行文本（与目录配置同一形态）。
func DialHTTP(ctx context.Context, endpoint, headers string) (*HTTPSession, error) {
	s := &HTTPSession{endpoint: strings.TrimSpace(endpoint), headers: parseHeaders(headers), client: http.DefaultClient}
	if s.endpoint == "" {
		return nil, errors.New("mcp http: 服务器地址为空")
	}
	raw, err := s.rpc(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "tiancode", "version": "0.0.45"},
	}, false)
	if err != nil {
		return nil, fmt.Errorf("mcp http 握手失败：%w", err)
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(raw, &init) == nil {
		s.version = init.ProtocolVersion
	}
	if _, err := s.rpc(ctx, "notifications/initialized", map[string]any{}, true); err != nil {
		return nil, fmt.Errorf("mcp http 握手失败：%w", err)
	}
	return s, nil
}

// ListTools 返回服务器公布的工具（含参数 schema）。
func (s *HTTPSession) ListTools(ctx context.Context) ([]Tool, error) {
	raw, err := s.rpc(ctx, "tools/list", map[string]any{}, false)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload.Tools, nil
}

// Call 调用一个工具；工具自身报错（isError）返回 *ToolError。
func (s *HTTPSession) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	raw, err := s.rpc(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, false)
	if err != nil {
		return "", err
	}
	return parseCallResult(raw)
}

// Close 结束会话（规范：客户端不再使用时应 DELETE；服务器可回 405，忽略即可）。
func (s *HTTPSession) Close() error {
	if s.sessionID == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodDelete, s.endpoint, nil)
	if err != nil {
		return err
	}
	s.applyHeaders(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (s *HTTPSession) applyHeaders(req *http.Request) {
	for k, vs := range s.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if s.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", s.sessionID)
	}
	if s.version != "" {
		req.Header.Set("MCP-Protocol-Version", s.version)
	}
}

// rpc 发一条 JSON-RPC 请求（notify=true 为通知：无 id，期望 202/200 无内容）。
func (s *HTTPSession) rpc(ctx context.Context, method string, params any, notify bool) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	id := 0
	if !notify {
		s.seq++
		id = s.seq
		msg["id"] = id
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	s.applyHeaders(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" && method == "initialize" {
		s.sessionID = sid
	}
	if resp.StatusCode == http.StatusNotFound && s.sessionID != "" {
		return nil, errors.New("mcp http: 会话已失效（服务器返回 404），请重试")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("mcp http %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	if notify {
		return nil, nil
	}
	body2 := io.LimitReader(resp.Body, httpResponseLimit)
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return readSSEResponse(body2, id)
	}
	raw, err := io.ReadAll(body2)
	if err != nil {
		return nil, err
	}
	return unwrapRPC(raw, id)
}

// readSSEResponse 从 SSE 流里找到 id 匹配的 JSON-RPC 响应（跳过服务器通知/请求）。
func readSSEResponse(r io.Reader, id int) (json.RawMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), httpResponseLimit)
	var data bytes.Buffer
	flush := func() (json.RawMessage, bool, error) {
		if data.Len() == 0 {
			return nil, false, nil
		}
		payload := append([]byte(nil), data.Bytes()...)
		data.Reset()
		res, err := unwrapRPC(payload, id)
		if errors.Is(err, errNotOurs) {
			return nil, false, nil
		}
		return res, true, err
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if res, done, err := flush(); done {
				return res, err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if res, done, err := flush(); done {
		return res, err
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("mcp http: 事件流结束但没有收到本次请求的响应")
}

var errNotOurs = errors.New("not our response")

// unwrapRPC 解出 JSON-RPC 响应的 result；id 不匹配（服务器通知等）返回 errNotOurs。
func unwrapRPC(raw []byte, id int) (json.RawMessage, error) {
	var env struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("mcp http: 响应不是合法 JSON-RPC：%s", strings.TrimSpace(string(raw[:min(len(raw), 200)])))
	}
	// id 可能回成数字或字符串（个别服务器把 id 当字符串回显）：两种都认
	if strings.Trim(string(env.ID), `"`) != fmt.Sprint(id) {
		return nil, errNotOurs
	}
	if env.Error != nil {
		return nil, fmt.Errorf("mcp: %s", env.Error.Message)
	}
	return env.Result, nil
}

// parseHeaders 解析"键=值"逐行文本。
func parseHeaders(text string) http.Header {
	h := http.Header{}
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(k) != "" {
			h.Set(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
	return h
}

// parseCallResult 把 tools/call 的 result 转成文本（多段 content 拼接）；isError → *ToolError。
// stdio 与 HTTP 共用（此前 HTTP 路径忽略 isError，工具报错被当成成功结果）。
func parseCallResult(raw json.RawMessage) (string, error) {
	var payload struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return string(raw), nil
	}
	var b strings.Builder
	for _, part := range payload.Content {
		if part.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(part.Text)
		}
	}
	text := b.String()
	if text == "" {
		text = string(raw)
	}
	if payload.IsError {
		return "", &ToolError{Message: text}
	}
	return text, nil
}
