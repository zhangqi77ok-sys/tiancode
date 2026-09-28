package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestClient_ListAndCall(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	go serveFakeMCP(right)

	c := New(left)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
	out, err := c.Call(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("out = %q", out)
	}
}

func TestCall_CancelUnblocksRead(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	go func() {
		srv := New(right)
		_, _ = srv.read() // 吃掉请求，不回复，让对端卡在读
	}()
	c := New(left)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := c.Call(ctx, "echo", json.RawMessage(`{}`))
		errCh <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消后仍卡在读，中断不会生效")
	}
}

func serveFakeMCP(conn net.Conn) {
	defer conn.Close()
	c := New(conn)
	for {
		msg, err := c.read()
		if err != nil {
			return
		}
		var env struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(msg, &env); err != nil || env.ID == nil {
			continue
		}
		var result any
		switch env.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": protocolVersion}
		case "tools/list":
			result = map[string]any{"tools": []map[string]string{{"name": "echo", "description": "echo"}}}
		case "tools/call":
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": "hi"}}}
		default:
			continue
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *env.ID, "result": result})
		_ = c.write(body)
	}
}
