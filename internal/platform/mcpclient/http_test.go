package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// strictServer 模拟按规范要求握手的 Streamable HTTP 服务器：没有 initialize 得到的
// 会话头就 400；tools/list 用 SSE 回（中间夹一条服务器通知）；tools/call 的 fail 工具回 isError。
func strictServer(t *testing.T) (*httptest.Server, *sync.Map) {
	t.Helper()
	seen := &sync.Map{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			seen.Store("deleted", r.Header.Get("Mcp-Session-Id"))
			w.WriteHeader(http.StatusOK)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		seen.Store(msg.Method, r.Header.Get("Mcp-Session-Id"))
		if msg.Method == "initialize" {
			w.Header().Set("Mcp-Session-Id", "sess-1")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{}}}`, msg.ID)
			return
		}
		if r.Header.Get("Mcp-Session-Id") != "sess-1" {
			http.Error(w, "missing session", http.StatusBadRequest)
			return
		}
		switch msg.Method {
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n")
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"tools\":[{\"name\":\"echo\",\"description\":\"回显\",\"inputSchema\":{\"type\":\"object\",\"properties\":{\"text\":{\"type\":\"string\"}}}}]}}\n\n", msg.ID)
		case "tools/call":
			w.Header().Set("Content-Type", "application/json")
			if msg.Params.Name == "fail" {
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"isError":true,"content":[{"type":"text","text":"坏参数"}]}}`, msg.ID)
				return
			}
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"ok"}]}}`, msg.ID)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

// 2026-10-19：远程服务器走完整握手（此前裸发 tools/call，要求握手的服务器一律拒绝）。
func TestHTTPSession_HandshakeListCall(t *testing.T) {
	srv, seen := strictServer(t)
	ctx := context.Background()
	s, err := DialHTTP(ctx, srv.URL, "Authorization=Bearer x")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := seen.Load("notifications/initialized"); v != "sess-1" {
		t.Fatalf("initialized 通知应带会话头：%v", v)
	}
	list, err := s.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "echo" || !strings.Contains(string(list[0].InputSchema), `"text"`) {
		t.Fatalf("SSE 响应应跳过通知、解出工具与 schema：%+v", list)
	}
	out, err := s.Call(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil || out != "ok" {
		t.Fatalf("调用结果不符：%q %v", out, err)
	}
	_, err = s.Call(ctx, "fail", nil)
	var te *ToolError
	if !errors.As(err, &te) || te.Message != "坏参数" {
		t.Fatalf("isError 应返回 ToolError（此前 HTTP 路径当成功）：%v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if v, _ := seen.Load("deleted"); v != "sess-1" {
		t.Fatalf("Close 应 DELETE 会话：%v", v)
	}
}
