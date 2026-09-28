// Package mcpclient 是最小的 MCP 客户端（stdio 帧 + JSON-RPC）。
//
// 做什么：initialize、tools/list、tools/call。远程 URL 用一次 HTTP POST。
// 被谁依赖：internal/platform/exttools。
// 依赖谁：stdlib。
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
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

const protocolVersion = "2024-11-05"

// Tool 是上游公布的一个工具。
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Client 在一条双向流上说 MCP。
type Client struct {
	rw  io.ReadWriteCloser
	r   *bufio.Reader
	mu  sync.Mutex
	seq int
}

// New 包装已有的双向连接（测试注入）。
func New(rw io.ReadWriteCloser) *Client {
	return &Client{rw: rw, r: bufio.NewReader(rw)}
}

// DialStdio 启动本地进程并用 stdin/stdout 通信。
func DialStdio(command, args, env string) (*Client, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("mcp: 缺少启动命令")
	}
	if runtime.GOOS == "windows" && !strings.Contains(command, ".") {
		if _, err := exec.LookPath(command); err != nil {
			if _, err2 := exec.LookPath(command + ".cmd"); err2 == nil {
				command += ".cmd"
			}
		}
	}
	cmd := exec.Command(command, strings.Fields(args)...)
	hideConsole(cmd)
	cmd.Env = os.Environ()
	for _, line := range strings.Split(env, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		cmd.Env = append(cmd.Env, line)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: 启动 %s 失败：%w", command, err)
	}
	pr, pw := io.Pipe()
	go func() {
		_, copyErr := io.Copy(pw, stdout)
		closeErr := pw.Close()
		if copyErr != nil || closeErr != nil {
			return
		}
	}()
	go func() { _ = cmd.Wait() }()
	return New(stdioRW{Reader: pr, WriteCloser: stdin, cmd: cmd}), nil
}

type stdioRW struct {
	io.Reader
	io.WriteCloser
	cmd *exec.Cmd
}

func (s stdioRW) Close() error {
	err1 := s.WriteCloser.Close()
	var err2 error
	if s.cmd != nil && s.cmd.Process != nil {
		// 按进程树杀（taskkill /T）：MCP 服务器是 cmd → node 的批处理链，
		// 只杀直接子进程会留下无主的 node.exe（见 killProcessTree 注释）。
		// 树杀失败通常意味着进程已经退出；此时再按单进程兜底一次，
		// 只有真的杀不动（进程仍在且拒绝结束）才算错误——否则上层的
		// MCPTool.Close 会拒绝摘除这条会话，留下一个死客户端在缓存里。
		if treeErr := killProcessTree(s.cmd.Process.Pid); treeErr != nil {
			if killErr := s.cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
				err2 = killErr
			}
		}
	}
	if err1 != nil {
		return err1
	}
	return err2
}

// Initialize 完成握手。
func (c *Client) Initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "tiancode", "version": "0.2.22"},
	})
	if err != nil {
		return err
	}
	return c.notify(ctx, "notifications/initialized", map[string]any{})
}

// ListTools 返回工具名。
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
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

// Call 调用一个工具，返回文本（多段 content 拼在一起）。
func (c *Client) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": json.RawMessage(args)})
	if err != nil {
		return "", err
	}
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
		return "", fmt.Errorf("%s", text)
	}
	return text, nil
}

func (c *Client) Close() error {
	if c.rw == nil {
		return nil
	}
	return c.rw.Close()
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	id := c.seq
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		return nil, err
	}
	if err := c.write(body); err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		msg, err := c.readCtx(ctx)
		if err != nil {
			return nil, err
		}
		var env struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(msg, &env); err != nil || env.ID == nil || *env.ID != id {
			continue
		}
		if env.Error != nil {
			return nil, fmt.Errorf("mcp: %s", env.Error.Message)
		}
		return env.Result, nil
	}
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	_ = ctx
	return c.write(body)
}

func (c *Client) write(body []byte) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(body))
	buf.Write(body)
	_, err := c.rw.Write(buf.Bytes())
	return err
}

// readCtx 读一帧；ctx 结束时关闭连接，让卡在管道上的读立刻返回。
// 为什么不能只看截止时间：read 在进程没吐字节时会一直堵住，截止时间要等这一帧读完才检查，
// 用户点中断时这一轮就停不下来。
func (c *Client) readCtx(ctx context.Context) (json.RawMessage, error) {
	type result struct {
		msg json.RawMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		msg, err := c.read()
		ch <- result{msg, err}
	}()
	select {
	case <-ctx.Done():
		closeErr := c.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("%w（关闭：%v）", ctx.Err(), closeErr)
		}
		return nil, ctx.Err()
	case r := <-ch:
		return r.msg, r.err
	}
}

func (c *Client) read() (json.RawMessage, error) {
	var length int
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			n, err := strconv.Atoi(strings.TrimSpace(line[len("content-length:"):]))
			if err != nil {
				return nil, err
			}
			length = n
		}
	}
	if length <= 0 || length > 8<<20 {
		return nil, fmt.Errorf("mcp: 非法 Content-Length %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// CallHTTP 对远程 MCP 发一次 tools/call（JSON 响应；若是 SSE 则取第一条 data）。
func CallHTTP(ctx context.Context, endpoint, headers, tool string, args json.RawMessage) (string, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": json.RawMessage(args)},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for _, line := range strings.Split(headers, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && k != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("mcp http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	payload := raw
	if bytes.Contains(raw, []byte("data:")) {
		for _, line := range bytes.Split(raw, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if bytes.HasPrefix(line, []byte("data:")) {
				payload = bytes.TrimSpace(line[len("data:"):])
				break
			}
		}
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return string(payload), nil
	}
	if env.Error != nil {
		return "", fmt.Errorf("mcp: %s", env.Error.Message)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		return string(env.Result), nil
	}
	var b strings.Builder
	for _, part := range result.Content {
		if part.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(part.Text)
		}
	}
	if b.Len() == 0 {
		return string(env.Result), nil
	}
	return b.String(), nil
}
